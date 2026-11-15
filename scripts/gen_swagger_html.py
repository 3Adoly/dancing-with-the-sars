"""Build api/swagger.html: a single self-contained Swagger UI page with api/openapi.yaml embedded.

Send the HTML file to the frontend team: they double-click it and get the interactive API docs.
"Try it out" calls the first server in the spec (the live team server) unless they pick another one.
Regenerate after every change to api/openapi.yaml:

    python scripts/gen_swagger_html.py
"""
import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SPEC = os.path.join(ROOT, "api", "openapi.yaml")
OUT = os.path.join(ROOT, "api", "swagger.html")

SWAGGER = "https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.17.14"
JSYAML = "https://cdnjs.cloudflare.com/ajax/libs/js-yaml/4.1.0/js-yaml.min.js"

TEMPLATE = """<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Dancing with the SARs API</title>
  <link rel="stylesheet" href="{swagger}/swagger-ui.min.css">
  <style>
    body {{ margin: 0; background: #fafafa; font-family: system-ui, sans-serif; }}
    .dwts-banner {{ background: #0b3d91; color: #fff; padding: 12px 20px; font-size: 14px; line-height: 1.5; }}
    .dwts-banner a {{ color: #ffd479; }}
    .dwts-banner code {{ background: rgba(255,255,255,.15); padding: 1px 5px; border-radius: 4px; }}
    #dwts-error {{ display: none; margin: 20px; padding: 12px; border: 1px solid #c00; color: #c00; background: #fff5f5; }}
  </style>
</head>
<body>
  <div class="dwts-banner">
    <strong>Dancing with the SARs: API for the frontend</strong> · NASA Space Apps 2026<br>
    Live server: <a href="http://164.92.235.173:8090/healthz">http://164.92.235.173:8090</a>
    (choose it in <em>Servers</em>, then <em>Try it out</em>). Status today: E1–E5 and E10–E14 work; E6–E9 answer
    <code>501</code> until the database lands; <code>/chat</code> is optional and disabled (<code>503</code>).
    Example responses: <code>api/examples/*.json</code> in the repo.
  </div>
  <div id="dwts-error"></div>
  <div id="swagger-ui"></div>

  <script>var OPENAPI_YAML = {spec};</script>
  <script src="{jsyaml}"></script>
  <script src="{swagger}/swagger-ui-bundle.min.js"></script>
  <script>
    (function () {{
      try {{
        var spec = jsyaml.load(OPENAPI_YAML);
        window.ui = SwaggerUIBundle({{
          spec: spec,
          dom_id: '#swagger-ui',
          deepLinking: true,
          docExpansion: 'list',
          defaultModelsExpandDepth: 0,
          displayRequestDuration: true,
          tryItOutEnabled: true,
          filter: true
        }});
      }} catch (e) {{
        var box = document.getElementById('dwts-error');
        box.style.display = 'block';
        box.textContent = 'Could not load the API spec: ' + e + ' (needs internet access for the Swagger UI library).';
      }}
    }})();
  </script>
</body>
</html>
"""


def main():
    with open(SPEC, encoding="utf-8") as f:
        spec = f.read()
    # Embed the YAML as a JS string literal. json.dumps gives a valid JS string; escaping "</" and "<!--"
    # stops the HTML parser from ending the <script> block early (they decode back to "</" and "<!--" in JS).
    literal = json.dumps(spec).replace("</", "<\\/").replace("<!--", "<\\!--")
    page = TEMPLATE.format(swagger=SWAGGER, jsyaml=JSYAML, spec=literal)
    with open(OUT, "w", encoding="utf-8", newline="\n") as f:
        f.write(page)
    print("wrote", os.path.relpath(OUT, ROOT), len(page), "bytes")


if __name__ == "__main__":
    main()
