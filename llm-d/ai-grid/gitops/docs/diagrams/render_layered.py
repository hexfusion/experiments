from pathlib import Path
from html import escape
import subprocess

OUT = Path(__file__).parent / 'layered'
OUT.mkdir(exist_ok=True)
INK = '#183349'
MUTED = '#526b7e'
BLUE = '#2463a5'
GREEN = '#147568'
PURPLE = '#8054b6'
GOLD = '#9a6d22'
parts = []

def start(w, h, title, description):
    parts.clear()
    parts.append(f'''<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img" aria-labelledby="title desc">
<title id="title">{escape(title)}</title><desc id="desc">{escape(description)}</desc>
<defs>
<marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="{BLUE}"/></marker>
<marker id="callout" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="{PURPLE}"/></marker>
<marker id="config" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M 0 0 L 10 5 L 0 10 z" fill="{GOLD}"/></marker>
</defs>
<rect width="100%" height="100%" fill="#fcfcfa"/>
<g font-family="DejaVu Sans, sans-serif" fill="{INK}">''')

def text(x, y, content, size=20, weight=400, color=INK, anchor='start'):
    parts.append(f'<text x="{x}" y="{y}" font-size="{size}" font-weight="{weight}" fill="{color}" text-anchor="{anchor}">{escape(content)}</text>')

def box(x, y, w, h, fill='white', stroke='#cbd7de', radius=26, dash=None):
    style = f' stroke-dasharray="{dash}"' if dash else ''
    parts.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{radius}" fill="{fill}" stroke="{stroke}" stroke-width="1.7"{style}/>')

def lines(x, y, content, size=19, gap=29, color=MUTED, weight=400):
    for i, line in enumerate(content):
        text(x, y + i * gap, line, size, weight, color)

def pill(x, y, label, color=BLUE, fill='#e4eefb', w=None):
    width = w or max(102, len(label)*13 + 32)
    box(x, y, width, 38, fill, fill, 19)
    text(x + width/2, y + 26, label, 19, 600, color, 'middle')
    return width

def arrow(path, mode='http', both=False):
    color, marker, dash, width = {
        'http': (BLUE, 'arrow', '', 3),
        'callout': (PURPLE, 'callout', '9 7', 2.4),
        'config': (GOLD, 'config', '3 6', 2.2),
    }[mode]
    extra = f' marker-start="url(#{marker})"' if both else ''
    parts.append(f'<path d="{path}" fill="none" stroke="{color}" stroke-width="{width}" stroke-dasharray="{dash}" marker-end="url(#{marker})"{extra} stroke-linejoin="round"/>')

def save(name, width):
    parts.append('</g></svg>')
    path = OUT / f'{name}.svg'
    source = '\n'.join(parts)
    if name == '01-model-placement':
        source = source.replace('height="1150" viewBox="0 0 1720 1150"', 'height="950" viewBox="0 115 1720 950"')
        source = source.replace('<rect width="100%" height="100%"', '<rect width="1720" height="1150"')
    path.write_text(source)
    subprocess.run(['rsvg-convert', '--width', str(width), '--output', str(OUT / f'{name}.png'), str(path)], check=True)

start(1720, 1150, 'Three model names. One MaaS provider. Two serving sites.',
      'Target architecture, illustrative model aliases. MaaS has three ExternalModels referencing grid. Grid knows four destinations from site-local InferenceProviders. KServe hosts granite and qwen locally, qwen and llama remotely. Model weights exist at serving sites.')

# The macro view puts model information alongside each routing layer.
box(50, 130, 1620, 220, '#edf4fd', '#b7cfe9', 38)
text(80, 169, '01   MaaS · model access', 23, 700, BLUE)
box(80, 195, 360, 125, 'white', '#b7cfe9')
text(105, 232, 'MaaS gateway', 24, 700)
lines(105, 263, ['Client requests a model name', 'Authorization + HTTP forwarding'], 17, 27)
text(510, 211, 'ExternalModel × 3', 22, 700)
pill(510, 232, 'granite'); pill(635, 232, 'qwen'); pill(750, 232, 'llama')
text(510, 305, 'Published names + MaaS access policy', 18, 400, MUTED)
arrow('M 890 251 L 1035 251', 'config')
text(925, 231, 'all reference', 16, 400, GOLD)
box(1060, 195, 565, 125, 'white', '#b7cfe9')
text(1085, 233, 'ExternalProvider / grid', 23, 700)
lines(1085, 267, ['One endpoint: Grid gateway', 'MaaS keeps no separate local / remote providers'], 18, 28)

arrow('M 260 350 L 260 414')
text(290, 389, 'HTTPS · requested model name', 17, 500, BLUE)

box(50, 420, 1620, 235, '#edf8f3', '#a8d0c1', 38)
text(80, 461, '02   Grid · destination selection', 23, 700, GREEN)
box(80, 488, 360, 128, 'white', '#a8d0c1')
text(105, 527, 'Grid gateway', 24, 700)
lines(105, 560, ['Praxis HTTP proxy', 'Choose a site + serving provider'], 18, 28)
text(510, 499, 'Routing catalog', 22, 700)
text(510, 527, 'Derived from per-site InferenceProvider objects', 17, 400, MUTED)
for y, model, sites in [(562, 'granite', 'Site A'), (597, 'qwen', 'Site A  or  Site B'), (632, 'llama', 'Site B')]:
    text(530, y, model, 20, 600)
    text(710, y, '→  ' + sites, 20, 400, GREEN)
box(1110, 486, 515, 130, '#f8fcf9', '#c8ded3')
text(1135, 522, 'Metadata available here', 21, 700)
lines(1135, 552, ['Model names · endpoints · hosting sites', 'Routing policy + available health/load signals', 'Remote catalog is learned; CRs remain per site'], 16, 25)

# Forwarding forks into local and remote sites.
arrow('M 260 655 L 260 717')
arrow('M 440 578 L 470 578 L 470 689 L 1260 689 L 1260 717')
text(520, 679, 'Remote selection → peer Grid gateway over mTLS', 17, 500, BLUE)
text(80, 694, 'Local selection', 16, 500, BLUE)

for x, site, model1, model2, remote in [(50, 'Site A · local', 'granite', 'qwen', False), (885, 'Site B · remote', 'qwen', 'llama', True)]:
    box(x, 725, 785, 320, '#f5f4fb', '#cbc7e2', 38)
    text(x+30, 767, site, 24, 700, PURPLE)
    text(x+30, 800, 'Remote Grid gateway → serving gateway' if remote else 'Serving gateway', 20, 600)
    text(x+30, 829, 'EPP selects a pod from its InferencePool', 18, 400, MUTED)
    box(x+25, 850, 735, 169, 'white', '#d8d5e6', 24)
    text(x+47, 883, 'LLMInferenceService', 20, 700)
    pill(x+47, 898, model1, PURPLE, '#eee9f8')
    pill(x+182, 898, model2, PURPLE, '#eee9f8')
    text(x+380, 883, 'InferenceProvider', 20, 700)
    pill(x+380, 898, model1, GREEN, '#e4f3eb')
    pill(x+515, 898, model2, GREEN, '#e4f3eb')
    arrow(f'M {x+322} 918 L {x+365} 918', 'config')
    text(x+47, 965, 'Model weights + inference run here', 18, 600)
    text(x+47, 993, '#966 publishes one provider per LLMInferenceService–Gateway pair', 15, 400, MUTED)

save('01-model-placement', 1720)

start(1960, 680, 'MaaS and Grid networking',
      'Illustrative target topology. ExternalModel qwen references ExternalProvider grid. Envoy calls Praxis over gRPC and sends HTTPS to Grid using the provider credential. Grid selects a local or remote serving gateway. EPP selects runtime pods. Model labels are aliases, not generated provider resource names.')

box(30, 30, 440, 620, '#edf4fd', '#b7cfe9', 36)
text(60, 72, 'MaaS', 26, 700, BLUE)
box(60, 100, 380, 105, 'white', '#d9c799', 24)
text(83, 140, 'ExternalModel · qwen', 22, 700)
text(83, 176, '→ ExternalProvider · grid', 21, 600)

box(60, 250, 230, 90, '#f4eefb', '#cbb6e2', 24)
text(175, 287, 'Praxis extproc', 23, 700, PURPLE, 'middle')
text(175, 318, 'pre / post', 19, 400, PURPLE, 'middle')
box(310, 250, 130, 90, '#f4eefb', '#cbb6e2', 24)
text(375, 303, 'Auth', 23, 700, PURPLE, 'middle')
arrow('M 175 400 L 175 345', 'callout', True)
text(65, 375, 'gRPC :9004', 16, 500, PURPLE)
arrow('M 375 400 L 375 345', 'callout', True)
text(290, 375, 'ext_authz', 16, 500, PURPLE)
arrow('M 175 205 L 175 245', 'config')
box(60, 410, 380, 110, 'white', '#7fa6d1', 26)
text(250, 454, 'Envoy', 29, 700, INK, 'middle')
text(250, 491, 'ext_proc → router', 22, 500, INK, 'middle')
text(60, 570, 'ProcessingRequest ⇄ ProcessingResponse', 16, 500, PURPLE)
text(60, 603, 'Replace caller credential', 18, 500, PURPLE)

# Protocol fields belong to the connection, not another proxy component.
text(510, 307, 'HTTPRoute → Service/provider-grid', 20, 600, GOLD)
text(510, 349, 'POST /v1/chat/completions', 22, 600, BLUE)
text(510, 389, 'HTTPS · grid-gateway.grid.svc:443', 20, 500, BLUE)
text(510, 498, 'Authorization: Bearer <Grid provider key>', 19, 500)
text(510, 535, '{"model":"qwen", "stream":true}', 20, 500)
text(707, 615, '200 · text/event-stream', 20, 500, BLUE, 'middle')

box(980, 30, 950, 620, '#edf8f3', '#a8d0c1', 36)
text(1010, 72, 'Grid', 26, 700, GREEN)
box(1005, 170, 260, 120, 'white', '#d9c799', 24)
text(1135, 214, '2 × InferenceProvider', 19, 700, INK, 'middle')
text(1135, 253, 'qwen · A / B', 22, 500, INK, 'middle')
arrow('M 1135 290 L 1135 405', 'config')
box(1005, 410, 260, 110, 'white', '#66aa96', 26)
text(1135, 447, 'Grid gateway', 25, 700, INK, 'middle')
text(1135, 479, 'Praxis HTTP proxy', 20, 500, INK, 'middle')

box(1295, 100, 610, 230, '#faf8fe', '#cbc7e2', 28)
text(1320, 141, 'Site A · local', 23, 700, PURPLE)
box(1575, 115, 105, 52, '#f0e9f8', '#cbb6e2', 20)
text(1627, 150, 'EPP', 22, 700, PURPLE, 'middle')
box(1320, 183, 225, 75, 'white', '#cbc7e2', 23)
text(1432, 227, 'Serving gateway', 21, 700, INK, 'middle')
box(1695, 183, 185, 75, 'white', '#cbc7e2', 23)
text(1787, 227, 'qwen', 24, 700, INK, 'middle')
arrow('M 1545 220 L 1688 220')
text(1617, 251, 'HTTP', 16, 500, BLUE, 'middle')
arrow('M 1520 183 L 1520 175 L 1627 175 L 1627 168', 'callout', True)
text(1320, 302, 'LLMInferenceService · qwen', 21, 600)
arrow('M 1265 440 L 1280 440 L 1280 220 L 1313 220')

box(1295, 360, 610, 265, '#faf8fe', '#cbc7e2', 28)
text(1320, 402, 'Site B · remote', 23, 700, PURPLE)
box(1560, 380, 105, 52, '#f0e9f8', '#cbb6e2', 20)
text(1612, 415, 'EPP', 22, 700, PURPLE, 'middle')
box(1320, 475, 140, 75, 'white', '#a8d0c1', 22)
text(1390, 519, 'Peer Grid', 22, 700, INK, 'middle')
box(1510, 475, 195, 75, 'white', '#cbc7e2', 22)
text(1607, 519, 'Serving gateway', 18, 700, INK, 'middle')
box(1755, 475, 125, 75, 'white', '#cbc7e2', 22)
text(1817, 519, 'qwen', 23, 700, INK, 'middle')
arrow('M 1265 488 L 1280 488 L 1280 512 L 1313 512')
text(1190, 565, 'mTLS', 18, 600, BLUE)
arrow('M 1460 512 L 1503 512')
arrow('M 1705 512 L 1748 512')
arrow('M 1612 475 L 1612 437', 'callout', True)
text(1320, 595, 'LLMInferenceService · qwen', 21, 600)
arrow('M 440 450 L 997 450')
arrow('M 1135 520 L 1135 580 L 485 580 L 485 505 L 448 505')

save('02-networking-closeup', 1960)


html = '''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>MaaS · Praxis · Grid — architecture</title><style>
body{margin:0;background:#edf1f4;color:#183349;font:16px system-ui,sans-serif}nav{position:sticky;top:0;display:flex;gap:12px;padding:14px 24px;background:#ffffffee;box-shadow:0 1px 8px #18334918;align-items:center}nav strong{margin-right:auto}a,button{color:#2463a5;background:white;border:1px solid #b7cfe9;border-radius:18px;padding:8px 16px;text-decoration:none;cursor:pointer;font:inherit}button.active{background:#2463a5;color:white}main{max-width:1800px;margin:18px auto;padding:0 16px}figure{margin:0}img{width:100%;height:auto;display:block;border-radius:22px;box-shadow:0 5px 24px #18334914}[hidden]{display:none}footer{padding:18px 24px;color:#526b7e;font-size:14px}
</style><nav><strong>MaaS / Grid architecture</strong><button class="active" onclick="show(0)">1 · Model placement</button><button onclick="show(1)">2 · Networking close-up</button><a id="download" href="01-model-placement.svg" download>Download SVG</a></nav>
<main><figure><img src="01-model-placement.svg" alt="Three model names exposed through MaaS, with local and remote serving placement"></figure><figure hidden><img src="02-networking-closeup.svg" alt="Envoy to Grid HTTP forwarding, Praxis and EPP gRPC callouts, and configuring Kubernetes objects"></figure></main>
<footer>Illustrative target architecture. Model aliases are granite, qwen and llama. Grid is the only MaaS provider for these models. Publication into Grid and registration into MaaS are separate operations. Model weights reside at serving sites. Sources: model-controller PR #966 at 7c4d60e3; AI Gateway controller at 55021648 plus the recorded Praxis configuration patch. Provider names and paths must follow KServe-reported status; aliases here simplify the view.</footer>
<script>function show(n){document.querySelectorAll('figure').forEach((e,i)=>e.hidden=i!==n);document.querySelectorAll('button').forEach((e,i)=>e.classList.toggle('active',i===n));document.querySelector('#download').href=['01-model-placement.svg','02-networking-closeup.svg'][n]}</script></html>'''
(OUT / 'index.html').write_text(html)
print(OUT)
