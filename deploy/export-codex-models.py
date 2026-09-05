#!/usr/bin/env python3
"""Export a complete Sub2API model catalog for Codex (Python 3.9+)."""

import argparse
import http.client
import ipaddress
import json
import math
import os
from pathlib import Path
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request


MAX_CATALOG_BYTES = 8 * 1024 * 1024


class ExportError(Exception):
    """A safe, public diagnostic without credentials or response content."""


class NoRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Never send the API key to a destination supplied by a redirect.
        return None


def catalog_url(base_url):
    if any(ord(character) <= 32 for character in base_url) or "\\" in base_url:
        raise ExportError("The base URL contains invalid characters.")
    try:
        parsed = urllib.parse.urlsplit(base_url)
        hostname = parsed.hostname
        parsed.port  # Validate malformed or out-of-range ports before making a request.
    except ValueError:
        raise ExportError("The base URL is invalid.") from None
    if (not hostname or parsed.username is not None or parsed.password is not None
            or parsed.query or parsed.fragment):
        raise ExportError("Use a base URL without credentials, a query, or a fragment.")
    loopback = hostname.lower() == "localhost"
    if not loopback:
        try:
            loopback = ipaddress.ip_address(hostname).is_loopback
        except ValueError:
            pass
    if parsed.scheme != "https" and not (parsed.scheme == "http" and loopback):
        raise ExportError("HTTPS is required except for loopback HTTP test servers.")
    prefix = parsed.path.rstrip("/")
    if prefix.endswith("/v1"):
        prefix = prefix[:-3]
    return urllib.parse.urlunsplit((
        parsed.scheme, parsed.netloc, prefix + "/backend-api/codex/models", "", "",
    ))


def api_key(auth_file):
    key = os.environ.get("OPENAI_API_KEY", "").strip()
    if not key:
        try:
            auth = json.loads(Path(auth_file).expanduser().read_text(encoding="utf-8"))
        except (OSError, ValueError, UnicodeError):
            raise ExportError("Cannot read the auth file; set OPENAI_API_KEY or provide a valid --auth-file.") from None
        key = auth.get("OPENAI_API_KEY") if isinstance(auth, dict) else None
    if not isinstance(key, str) or not key.strip():
        raise ExportError("No OPENAI_API_KEY was found in the environment or auth file.")
    key = key.strip()
    if any(ord(character) < 33 or ord(character) > 126 for character in key):
        raise ExportError("OPENAI_API_KEY contains invalid characters.")
    return key


def reject_non_json_constant(value):
    raise ValueError("Non-JSON numeric constant")


def validate_model_fields(model):
    # Match ModelInfo's non-optional, non-defaulted fields; preserve unknown metadata.
    required_types = {
        "display_name": str, "supported_reasoning_levels": list,
        "shell_type": str, "visibility": str, "supported_in_api": bool,
        "priority": int, "support_verbosity": bool, "truncation_policy": dict,
        "experimental_supported_tools": list,
    }
    for field, expected_type in required_types.items():
        if type(model.get(field)) is not expected_type:
            raise ExportError("A model is missing a required field or has an invalid type: {}.".format(field))
    if model["shell_type"] not in ("unified_exec", "default", "local", "shell_command", "disabled"):
        raise ExportError("A model has an unsupported shell_type.")
    if model["visibility"] not in ("list", "hide", "none"):
        raise ExportError("A model has an unsupported visibility.")
    if not -(2 ** 31) <= model["priority"] < 2 ** 31:
        raise ExportError("A model priority must fit a signed 32-bit integer.")
    if not all(isinstance(tool, str) for tool in model["experimental_supported_tools"]):
        raise ExportError("A model's experimental_supported_tools must contain only strings.")
    for level in model["supported_reasoning_levels"]:
        if (not isinstance(level, dict) or not isinstance(level.get("effort"), str)
                or not level["effort"] or not isinstance(level.get("description"), str)):
            raise ExportError("Every supported reasoning level must have an effort and description string.")
    policy = model["truncation_policy"]
    if (policy.get("mode") not in ("bytes", "tokens") or type(policy.get("limit")) is not int
            or not -(2 ** 63) <= policy["limit"] < 2 ** 63):
        raise ExportError("A model truncation_policy must have a valid mode and signed 64-bit limit.")


def validate_catalog(payload):
    try:
        catalog = json.loads(payload, parse_constant=reject_non_json_constant)
    except (ValueError, UnicodeError, RecursionError):
        raise ExportError("The server did not return a valid JSON model catalog.") from None
    models = catalog.get("models") if isinstance(catalog, dict) else None
    if not isinstance(models, list) or not models:
        raise ExportError("The response must contain a nonempty models list.")
    for model in models:
        if not isinstance(model, dict) or not isinstance(model.get("slug"), str) or not model["slug"].strip():
            raise ExportError("Every model must have a nonempty slug.")
        validate_model_fields(model)
        if model["slug"] == "gpt-6-astra":
            levels = model.get("supported_reasoning_levels")
            supports_max = isinstance(levels, list) and any(
                isinstance(level, dict) and level.get("effort") == "max" for level in levels
            )
            if model.get("multi_agent_reasoning_effort") != "max" or not supports_max:
                raise ExportError("The Astra catalog must support max and set multi_agent_reasoning_effort to max.")


def download_catalog(url, key, timeout):
    request = urllib.request.Request(url, headers={
        "Authorization": "Bearer " + key,
        "Accept": "application/json",
        "User-Agent": "sub2api-model-catalog-exporter",
    })
    opener = urllib.request.build_opener(NoRedirects())
    try:
        with opener.open(request, timeout=timeout) as response:
            if response.status != 200:
                raise ExportError("The model catalog request did not return HTTP 200.")
            declared_length = response.headers.get("Content-Length")
            if response.headers.get("Transfer-Encoding", "").lower() == "chunked":
                declared_length = None
            if declared_length is not None:
                declared_length = int(declared_length)
                if not 0 <= declared_length <= MAX_CATALOG_BYTES:
                    raise ExportError("The model catalog has an invalid or oversized Content-Length.")
            payload = response.read(MAX_CATALOG_BYTES + 1)
            if declared_length is not None and len(payload) != declared_length:
                raise ExportError("The model catalog response was truncated.")
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        raise ExportError("The model catalog request failed (HTTP {}).".format(status)) from None
    except (urllib.error.URLError, OSError, http.client.HTTPException, ValueError, UnicodeError):
        raise ExportError("Cannot download the model catalog; check the URL, TLS certificate, and network connection.") from None
    if len(payload) > MAX_CATALOG_BYTES:
        raise ExportError("The model catalog exceeds the 8 MiB size limit.")
    validate_catalog(payload)
    return payload


def save_catalog(output, payload):
    temporary = None
    try:
        output = Path(output).expanduser().resolve()
        output.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.NamedTemporaryFile(mode="wb", prefix=".sub2api-models-", suffix=".tmp",
                                         dir=str(output.parent), delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(str(temporary), str(output))
        temporary = None
        return output
    except (OSError, ValueError, UnicodeError):
        raise ExportError("Cannot save the model catalog; check the output directory and permissions.") from None
    finally:
        if temporary is not None:
            try:
                temporary.unlink()
            except OSError:
                pass


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, help="Sub2API HTTPS base URL, optionally ending in /v1")
    parser.add_argument("--output", default="~/.codex/sub2api-models.json", help="Destination model catalog JSON")
    parser.add_argument("--auth-file", default="~/.codex/auth.json", help="Fallback auth JSON containing OPENAI_API_KEY")
    parser.add_argument("--timeout", default="30", help="Network timeout in seconds (default: 30, maximum: 300)")
    args = parser.parse_args(argv)
    try:
        try:
            timeout = float(args.timeout)
        except ValueError:
            raise ExportError("The timeout must be a number greater than zero and at most 300 seconds.") from None
        if not math.isfinite(timeout) or not 0 < timeout <= 300:
            raise ExportError("The timeout must be a number greater than zero and at most 300 seconds.")
        payload = download_catalog(catalog_url(args.base_url), api_key(args.auth_file), timeout)
        output = save_catalog(args.output, payload)
    except ExportError as error:
        print("Error: {}".format(error), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("Error: Model catalog export interrupted.", file=sys.stderr)
        return 130
    print("model_catalog_json = " + json.dumps(str(output)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
