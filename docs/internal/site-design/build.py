"""Build sdlc-site-looks.html from site-preview.src.html and dashboard.png.

Run from any folder: python3 build.py
The source holds the token DASHBOARD_DATA_URI once. The build replaces it
with the screenshot as a data URI, so the page needs no other file.
"""
import base64
import pathlib

here = pathlib.Path(__file__).resolve().parent
src = (here / "site-preview.src.html").read_text()
if src.count("DASHBOARD_DATA_URI") != 1:
    raise SystemExit("expected the token DASHBOARD_DATA_URI exactly once in the source")
uri = "data:image/png;base64," + base64.b64encode((here / "dashboard.png").read_bytes()).decode()
out = here / "sdlc-site-looks.html"
out.write_text(src.replace("DASHBOARD_DATA_URI", uri))
print("built", out, out.stat().st_size, "bytes")
