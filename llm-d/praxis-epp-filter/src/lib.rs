// SPDX-License-Identifier: MIT

//! A Praxis filter that gets routing decisions from llm-d's EPP without Praxis
//! implementing ext_proc.
//!
//! The filter calls `epp-http-gateway` over plain HTTP. The gateway speaks
//! ext_proc to an unmodified EPP and returns the decision. Praxis never opens a
//! bidirectional gRPC stream, never runs a per-phase state machine, and never
//! learns which body-mutation shape is legal in which processing mode.
//!
//! Two things about EPP's behavior are load-bearing here and are not incidental
//! details of the transport:
//!
//! EPP re-marshals the request. It parses the body into a map and re-serializes
//! it, so key order changes and integers above 2^53 lose precision. The body it
//! returns is the one to forward; forwarding the original silently drops model
//! rewrites. This filter replaces the body when the gateway reports it changed.
//!
//! EPP can decline a request. Load shedding and admission control arrive as an
//! immediate response, which this filter turns into a rejection rather than a
//! routing decision.

use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use praxis_filter::{
    BodyAccess, FilterAction, FilterError, HttpFilter, HttpFilterContext,
};
use serde::{Deserialize, Serialize};

/// Namespace for metadata this filter publishes, so a later filter reads a
/// known key rather than guessing.
pub const METADATA_NS: &str = "llmd.epp";

/// Header EPP sets to name the chosen endpoint.
const DESTINATION_HEADER: &str = "x-gateway-destination-endpoint";

#[derive(Debug, Clone, Deserialize)]
pub struct Config {
    /// Base URL of the routing gateway, for example `http://127.0.0.1:9100`.
    pub gateway: String,
    /// Request path reported to EPP, which routes on it.
    #[serde(default = "default_path")]
    pub path: String,
    #[serde(default = "default_timeout_ms")]
    pub timeout_ms: u64,
    /// When the gateway is unreachable, continue without a decision rather than
    /// failing the request. Off by default: routing silently degrading to
    /// whatever the next filter picks is worse than a visible error.
    #[serde(default)]
    pub fail_open: bool,
}

fn default_path() -> String {
    "/v1/chat/completions".to_string()
}

fn default_timeout_ms() -> u64 {
    5_000
}

/// Decision returned by the gateway, carried in a response header.
#[derive(Debug, Default, Deserialize, Serialize)]
struct Decision {
    #[serde(default)]
    destination: String,
    #[serde(default)]
    set_headers: std::collections::HashMap<String, String>,
    #[serde(default)]
    body_modified: bool,
    #[serde(default)]
    epp_duration_ms: f64,
}

pub struct EppRouterFilter {
    config: Config,
    client: reqwest::Client,
}

impl EppRouterFilter {
    pub fn new(config: Config) -> Result<Self, FilterError> {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_millis(config.timeout_ms))
            .build()
            .map_err(|e| -> FilterError { format!("http client: {e}").into() })?;
        Ok(Self { config, client })
    }

    pub fn from_config(config: &serde_yaml::Value) -> Result<Box<dyn HttpFilter>, FilterError> {
        let cfg: Config = serde_yaml::from_value(config.clone())
            .map_err(|e| -> FilterError { format!("epp_router config: {e}").into() })?;
        Ok(Box::new(Self::new(cfg)?))
    }
}

#[async_trait]
impl HttpFilter for EppRouterFilter {
    fn name(&self) -> &'static str {
        "epp_router"
    }

    /// The whole body is needed, because EPP parses it, and write access is
    /// needed because EPP hands back a re-serialized copy that must be
    /// forwarded in place of the original.
    fn request_body_access(&self) -> BodyAccess {
        BodyAccess::ReadWrite
    }

    async fn on_request(&self, _ctx: &mut HttpFilterContext<'_>) -> Result<FilterAction, FilterError> {
        Ok(FilterAction::Continue)
    }

    async fn on_request_body(
        &self,
        ctx: &mut HttpFilterContext<'_>,
        body: &mut Option<Bytes>,
        end_of_stream: bool,
    ) -> Result<FilterAction, FilterError> {
        if !end_of_stream {
            return Ok(FilterAction::Continue);
        }
        let payload = body.clone().unwrap_or_default();

        let url = format!("{}/v1/route", self.config.gateway.trim_end_matches('/'));
        let sent = self
            .client
            .post(&url)
            .header("content-type", "application/json")
            .header("x-original-path", &self.config.path)
            .header("x-original-method", "POST")
            .body(payload.clone())
            .send()
            .await;

        let resp = match sent {
            Ok(r) => r,
            Err(e) => {
                if self.config.fail_open {
                    tracing::warn!(error = %e, "epp gateway unreachable, continuing without a decision");
                    return Ok(FilterAction::Continue);
                }
                return Err(format!("epp gateway: {e}").into());
            }
        };

        let status = resp.status();
        let decision_hdr = resp
            .headers()
            .get("x-routing-decision")
            .and_then(|v| v.to_str().ok())
            .map(str::to_owned);

        let returned = resp
            .bytes()
            .await
            .map_err(|e| -> FilterError { format!("epp gateway body: {e}").into() })?;

        // EPP declined. Surface its status rather than routing anyway.
        if !status.is_success() {
            let reason = String::from_utf8_lossy(&returned).to_string();
            tracing::info!(status = status.as_u16(), "epp declined the request");
            ctx.set_metadata(format!("{METADATA_NS}.declined"), reason.clone());
            return Ok(FilterAction::Reject(
                praxis_filter::Rejection::status(status.as_u16()).with_body(reason),
            ));
        }

        let decision: Decision = decision_hdr
            .as_deref()
            .map(|raw| serde_json::from_str(raw))
            .transpose()
            .map_err(|e| -> FilterError { format!("decode decision: {e}").into() })?
            .unwrap_or_default();

        if decision.destination.is_empty() {
            if self.config.fail_open {
                return Ok(FilterAction::Continue);
            }
            return Err(FilterError::from("upstream processor returned no destination"));
        }

        // Publish for later filters and for the load balancer.
        ctx.set_metadata(DESTINATION_HEADER.to_string(), decision.destination.clone());
        ctx.set_structured_metadata(
            METADATA_NS,
            "destination",
            serde_json::Value::String(decision.destination.clone()),
        );
        ctx.set_structured_metadata(
            METADATA_NS,
            "epp_duration_ms",
            serde_json::json!(decision.epp_duration_ms),
        );
        for (k, v) in &decision.set_headers {
            ctx.set_metadata(k.clone(), v.clone());
        }

        // Forward what EPP returned, not what arrived, whenever they differ.
        if decision.body_modified && returned != payload {
            ctx.set_structured_metadata(METADATA_NS, "body_rewritten", serde_json::Value::Bool(true));
            *body = Some(returned);
        }

        Ok(FilterAction::Continue)
    }
}

praxis_filter::export_filters! {
    http "epp_router" => EppRouterFilter::from_config,
}
