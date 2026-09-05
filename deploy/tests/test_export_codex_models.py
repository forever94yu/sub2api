"""Network and filesystem regression tests for the standalone catalog exporter."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock


SCRIPT = Path(__file__).resolve().parents[1] / "export-codex-models.py"
ENV_KEY = "sk-export-environment-secret"
FILE_KEY = "sk-export-auth-file-secret"
PRIVATE_TEXT = "private-upstream-prompt-do-not-print"


def catalog():
    return {
        "models": [{
            "slug": "gpt-6-astra",
            "display_name": "GPT-6-Astra",
            "shell_type": "unified_exec",
            "visibility": "list",
            "supported_in_api": True,
            "priority": 0,
            "support_verbosity": False,
            "truncation_policy": {"mode": "bytes", "limit": 10000},
            "experimental_supported_tools": [],
            "default_reasoning_level": "low",
            "multi_agent_reasoning_effort": "max",
            "supported_reasoning_levels": [
                {"effort": "xhigh", "description": "Extra high"},
                {"effort": "max", "description": "Maximum"},
            ],
            "base_instructions": PRIVATE_TEXT,
            "future_metadata": {"preserve": True},
        }],
        "future_envelope_field": [1, 2, 3],
    }


@contextlib.contextmanager
def mock_server(body=None, status=200, headers=None, delay=0):
    requests = []
    payload = json.dumps(catalog()).encode() if body is None else body

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            requests.append((self.path, self.headers.get("Authorization")))
            time.sleep(delay)
            try:
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                for name, value in (headers or {}).items():
                    self.send_header(name, value)
                self.end_headers()
                self.wfile.write(payload)
            except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                pass

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
    worker.start()
    try:
        yield "http://127.0.0.1:{}".format(server.server_port), requests
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)


class ExportCodexModelsTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.output = self.directory / "catalog.json"
        self.auth_file = self.directory / "auth.json"
        self.auth_file.write_text(json.dumps({"OPENAI_API_KEY": FILE_KEY}), encoding="utf-8")
        self.environment = os.environ.copy()
        for name in list(self.environment):
            if name.lower().endswith("_proxy") or name == "OPENAI_API_KEY":
                self.environment.pop(name)
        self.environment.update({
            "OPENAI_API_KEY": ENV_KEY,
            "HOME": str(self.directory),
            "USERPROFILE": str(self.directory),
            "PYTHONDONTWRITEBYTECODE": "1",
        })

    def run_export(self, base_url, *extra, defaults=False):
        args = [sys.executable, "-B", str(SCRIPT), "--base-url", base_url]
        if not defaults:
            args += ["--output", str(self.output), "--auth-file", str(self.auth_file)]
        result = subprocess.run(
            args + list(extra), env=self.environment, capture_output=True,
            text=True, encoding="utf-8", timeout=10,
        )
        combined = result.stdout + result.stderr
        for secret in (ENV_KEY, FILE_KEY, PRIVATE_TEXT):
            self.assertNotIn(secret, combined)
        self.assertNotIn("Traceback", combined)
        return result

    def assert_failure_preserves(self, result):
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertTrue(result.stderr)
        self.assertEqual(self.output.read_bytes(), b"previous valid catalog")
        self.assertEqual(sorted(p.name for p in self.directory.iterdir()), ["auth.json", "catalog.json"])

    def test_export_preserves_complete_catalog_and_environment_key_wins(self):
        self.auth_file.write_text("invalid ignored auth file", encoding="utf-8")
        self.output.write_bytes(b"previous valid catalog")
        with mock_server() as (base_url, requests):
            result = self.run_export(base_url)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.output.read_text(encoding="utf-8")), catalog())
        self.assertEqual(requests, [("/backend-api/codex/models", "Bearer " + ENV_KEY)])
        self.assertEqual(result.stdout.strip(), "model_catalog_json = " + json.dumps(str(self.output.resolve())))
        self.assertEqual(result.stderr, "")

    def test_v1_suffix_and_reverse_proxy_prefix(self):
        for suffix, expected in [
            ("/v1", "/backend-api/codex/models"),
            ("/v1/", "/backend-api/codex/models"),
            ("/proxy/sub2api/v1/", "/proxy/sub2api/backend-api/codex/models"),
            ("/proxy/sub2api", "/proxy/sub2api/backend-api/codex/models"),
            ("/proxy/v10", "/proxy/v10/backend-api/codex/models"),
        ]:
            with self.subTest(suffix=suffix), mock_server() as (base_url, requests):
                result = self.run_export(base_url + suffix)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(requests[0][0], expected)

    def test_auth_file_fallback(self):
        self.environment.pop("OPENAI_API_KEY")
        with mock_server() as (base_url, requests):
            result = self.run_export(base_url)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(requests[0][1], "Bearer " + FILE_KEY)

    def test_defaults_expand_home_without_changing_config(self):
        self.environment.pop("OPENAI_API_KEY")
        codex_directory = self.directory / ".codex"
        codex_directory.mkdir()
        (codex_directory / "auth.json").write_bytes(self.auth_file.read_bytes())
        config = codex_directory / "config.toml"
        config.write_bytes(b"existing config remains intact")
        with mock_server() as (base_url, requests):
            result = self.run_export(base_url, defaults=True)
        output = codex_directory / "sub2api-models.json"
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(output.read_text(encoding="utf-8")), catalog())
        self.assertEqual(requests[0][1], "Bearer " + FILE_KEY)
        self.assertEqual(config.read_bytes(), b"existing config remains intact")
        self.assertEqual(result.stdout.strip(), "model_catalog_json = " + json.dumps(str(output.resolve())))

    def test_missing_or_invalid_credentials_make_no_request(self):
        self.environment.pop("OPENAI_API_KEY")
        for content in ["not JSON", "[]", "{}", '{"OPENAI_API_KEY": 123}', '{"OPENAI_API_KEY": ""}']:
            with self.subTest(content=content), mock_server() as (base_url, requests):
                self.auth_file.write_text(content, encoding="utf-8")
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))
                self.assertEqual(requests, [])

    def test_http_errors_and_bad_json_preserve_existing_file(self):
        for status, body in [(401, PRIVATE_TEXT.encode()), (500, PRIVATE_TEXT.encode()), (200, b"not json")]:
            with self.subTest(status=status, body=body), mock_server(body=body, status=status) as (base_url, requests):
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))
                self.assertEqual(len(requests), 1)

    def test_rejects_invalid_model_lists_and_astra_ultra_target(self):
        bad_target = catalog()
        bad_target["models"][0]["multi_agent_reasoning_effort"] = "xhigh"
        missing_max = catalog()
        missing_max["models"][0]["supported_reasoning_levels"] = [{"effort": "xhigh", "description": "Extra high"}]
        invalid_catalogs = [[], {}, {"models": []}, {"models": ["astra"]},
                            {"models": [{}]}, {"models": [{"slug": "gpt", "invalid": float("nan")}]},
                            bad_target, missing_max]
        for payload in invalid_catalogs:
            with self.subTest(payload=payload), mock_server(body=json.dumps(payload).encode()) as (base_url, requests):
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))
                self.assertEqual(len(requests), 1)

    def test_catalog_without_astra_is_exported_without_inventing_models(self):
        payload = catalog()
        payload["models"][0]["slug"] = "another-model"
        payload["models"][0].pop("multi_agent_reasoning_effort")
        with mock_server(body=json.dumps(payload).encode()) as (base_url, _):
            result = self.run_export(base_url)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.output.read_text(encoding="utf-8")), payload)

    def test_missing_required_model_fields_preserve_existing_file(self):
        for field in ("slug", "display_name", "supported_reasoning_levels", "shell_type",
                      "visibility", "supported_in_api", "priority", "support_verbosity",
                      "truncation_policy", "experimental_supported_tools"):
            payload = catalog()
            payload["models"][0]["slug"] = "another-model"
            del payload["models"][0][field]
            with self.subTest(field=field), mock_server(body=json.dumps(payload).encode()) as (base_url, requests):
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))
                self.assertEqual(len(requests), 1)

    def test_invalid_required_model_field_types_preserve_existing_file(self):
        invalid_values = [
            ("display_name", None), ("shell_type", []), ("shell_type", "invalid-shell"),
            ("visibility", 1), ("visibility", "invalid-visibility"),
            ("supported_in_api", "true"), ("supported_in_api", 1),
            ("priority", True), ("priority", 1.5), ("priority", 2 ** 31),
            ("support_verbosity", 0), ("experimental_supported_tools", {}),
            ("experimental_supported_tools", [False]), ("supported_reasoning_levels", {}),
            ("supported_reasoning_levels", ["max"]),
            ("supported_reasoning_levels", [{"effort": "max"}]),
            ("supported_reasoning_levels", [{"description": "Maximum"}]),
            ("supported_reasoning_levels", [{"effort": "", "description": "Maximum"}]),
            ("supported_reasoning_levels", [{"effort": "max", "description": 1}]),
            ("truncation_policy", []), ("truncation_policy", {"limit": 10}),
            ("truncation_policy", {"mode": "bytes"}),
            ("truncation_policy", {"mode": "invalid-mode", "limit": 10}),
            ("truncation_policy", {"mode": "bytes", "limit": True}),
            ("truncation_policy", {"mode": "bytes", "limit": 2 ** 63}),
        ]
        for field, value in invalid_values:
            payload = catalog()
            payload["models"][0]["slug"] = "another-model"
            payload["models"][0][field] = value
            with self.subTest(field=field, value=value), mock_server(body=json.dumps(payload).encode()) as (base_url, requests):
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))
                self.assertEqual(len(requests), 1)

    def test_redirect_cannot_forward_credentials_to_another_origin(self):
        with mock_server() as (destination, destination_requests):
            with mock_server(status=302, headers={"Location": destination + "/capture"}) as (base_url, requests):
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))
        self.assertEqual(len(requests), 1)
        self.assertEqual(destination_requests, [])

    def test_rejects_remote_plain_http_url_credentials_and_queries(self):
        for base_url in ["http://example.com", "ftp://127.0.0.1", "https://user:password@example.com",
                         "https://example.com?secret=" + ENV_KEY, "https://example.com/#fragment"]:
            with self.subTest(base_url=base_url):
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))

    def test_timeout_preserves_file_without_logging_body(self):
        self.output.write_bytes(b"previous valid catalog")
        with mock_server(delay=0.2) as (base_url, requests):
            result = self.run_export(base_url, "--timeout", "0.05")
        self.assert_failure_preserves(result)
        self.assertEqual(len(requests), 1)

    def test_oversized_and_truncated_responses_preserve_existing_file(self):
        valid_body = json.dumps(catalog()).encode()
        for body, headers in [(b" " * (8 * 1024 * 1024 + 1), {}),
                              (valid_body, {"Content-Length": str(len(valid_body) + 100)})]:
            with self.subTest(headers=headers), mock_server(body=body, headers=headers) as (base_url, requests):
                self.output.write_bytes(b"previous valid catalog")
                self.assert_failure_preserves(self.run_export(base_url))
                self.assertEqual(len(requests), 1)

    def test_invalid_environment_key_cannot_inject_headers(self):
        self.environment["OPENAI_API_KEY"] = ENV_KEY + "\nX-Injected: " + PRIVATE_TEXT
        self.output.write_bytes(b"previous valid catalog")
        with mock_server() as (base_url, requests):
            self.assert_failure_preserves(self.run_export(base_url))
        self.assertEqual(requests, [])

    def test_failed_atomic_replace_preserves_existing_file_and_removes_temporary(self):
        self.assertTrue(SCRIPT.is_file(), "catalog exporter is not implemented")
        spec = importlib.util.spec_from_file_location("export_codex_models", SCRIPT)
        exporter = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(exporter)
        self.output.write_bytes(b"previous valid catalog")
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock_server() as (base_url, _), mock.patch.dict(os.environ, self.environment, clear=True):
            with mock.patch.object(exporter.os, "replace", side_effect=OSError(PRIVATE_TEXT)):
                with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    status = exporter.main(["--base-url", base_url, "--output", str(self.output)])
        self.assertNotEqual(status, 0)
        self.assertEqual(stdout.getvalue(), "")
        self.assertNotIn(PRIVATE_TEXT, stderr.getvalue())
        self.assertEqual(self.output.read_bytes(), b"previous valid catalog")
        self.assertEqual(sorted(p.name for p in self.directory.iterdir()), ["auth.json", "catalog.json"])


if __name__ == "__main__":
    unittest.main()
