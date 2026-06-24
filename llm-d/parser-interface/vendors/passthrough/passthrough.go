package passthrough

import (
	"context"

	"prototype/kv"
	"prototype/parser"
)

var Vendor = parser.Vendor{
	Name:   "passthrough",
	Parser: parserImpl{},
}

type parserImpl struct{}

func (parserImpl) Parse(_ context.Context, body []byte) (parser.InferenceInput, error) {
	return &request{body: body, BaseAttrs: kv.NewBaseAttrs()}, nil
}

type request struct {
	body []byte
	kv.BaseAttrs
}

func (*request) Model() string    { return "" }
func (*request) Stream() bool     { return false }
func (r *request) Prompt() []byte { return r.body }
