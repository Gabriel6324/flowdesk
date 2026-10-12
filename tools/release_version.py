"""Read the software version from the backend's single authoritative constant."""
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
match = re.search(r'^const Version = "([0-9]+\.[0-9]+\.[0-9]+)"$', (ROOT / "internal/buildinfo/info.go").read_text(), re.MULTILINE)
if match is None:
    raise RuntimeError("Missing valid backend software version.")
VERSION = match.group(1)
