"""Сборка и проверка демо в отдельной временной папке без сохранения входных данных."""

import argparse
import hashlib
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    parser = argparse.ArgumentParser(description="Проверка стандартного демо Go")
    parser.add_argument("--go", default="go", help="путь к компилятору Go 1.27.2 или новее")
    args = parser.parse_args()
    env = {**os.environ, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
           "CGO_ENABLED": "0", "GOCACHE": str(ROOT / ".cache/go-build")}
    with tempfile.TemporaryDirectory(prefix="freecode-stream-demo-") as folder:
        temp = Path(folder)
        binary = temp / ("streammetrics.exe" if os.name == "nt" else "streammetrics")
        subprocess.run([args.go, "build", "-trimpath", "-buildvcs=false", "-o", str(binary), "./cmd/streammetrics"],
                       cwd=ROOT, env=env, capture_output=True, check=True)
        source, output = temp / "requests.jsonl", temp / "summary.json"

        def command(*arguments):
            completed = subprocess.run([str(binary), *map(str, arguments), "-json"], check=True,
                                       capture_output=True, encoding="utf-8", cwd=ROOT)
            return json.loads(completed.stdout)

        command("demo", "-output", source)
        first = command("run", "-input", source, "-output", output)
        second = command("run", "-input", source)
        assert first == second
        assert (first["input_rows"], first["accepted_rows"], first["rejected_rows"]) == (10005, 10000, 5)
        groups = first["services"]
        assert [group["service"] for group in groups] == ["api", "gateway", "worker"]
        assert sum(group["requests"] for group in groups) == 10000
        assert sum(group["server_errors"] for group in groups) == 500
        assert sum(group["client_errors"] for group in groups) == 300
        assert sum(group["slow_requests"] for group in groups) == 5010
        assert sum(sum(group["bucket_counts"]) for group in groups) == 10000
        assert all(group["p95_upper_ms"] == 1000 for group in groups)
        assert first["rejection_reasons"] == {"json": 1, "schema": 1, "service": 1, "status": 1, "duration_ms": 1}
        with source.open("rb") as stream:
            assert first["source_sha256"] == hashlib.file_digest(stream, "sha256").hexdigest()
        assert first["input_bytes"] == source.stat().st_size
        assert json.loads(output.read_text(encoding="utf-8")) == first
        before = output.read_bytes()
        refused = subprocess.run([str(binary), "run", "-input", str(source), "-output", str(output)], capture_output=True)
        assert refused.returncode == 1 and output.read_bytes() == before
    print("Демо проверено: 10 000 принятых строк, 5 ошибок, 3 сервиса; повтор совпал, существующий результат сохранён.")


if __name__ == "__main__":
    main()
