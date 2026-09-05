# Codex Ultra and the Local Model Catalog

Codex converts the local `ultra` selection into a native reasoning effort before
sending a Responses request. For GPT-6-Astra, Sub2API's Codex manifest advertises
`multi_agent_reasoning_effort = "max"` and includes `max` in the supported levels.
An explicit `ultra` request to Sub2API is also normalized to `max`.

Some Codex clients using API key authentication do not refresh the remote model
catalog. A bundled catalog can instead specify `xhigh` for Ultra. In that case,
Sub2API receives `xhigh`, with no field identifying the original Ultra selection.
A server upgrade alone cannot repair that client's already loaded catalog.

## Load the Gateway Catalog

Run the exporter on the computer running Codex, with Python 3.9 or newer:

```sh
python deploy/export-codex-models.py --base-url https://your-sub2api.example/v1
```

The exporter uses `OPENAI_API_KEY` from the environment, or the existing
`~/.codex/auth.json` file. `--auth-file` and `--output` accept alternate paths.
Use a Sub2API key assigned to an OpenAI or compatible Composite group.

After a successful export, add the printed `model_catalog_json` setting to the
top level of the active Codex `config.toml`, before any `[section]`. Keep your
existing provider and model settings. For Astra Ultra, the relevant settings are:

```toml
model = "gpt-6-astra"
model_reasoning_effort = "ultra"
# Use the actual absolute path printed by the exporter:
model_catalog_json = "/home/your-user/.codex/sub2api-models.json"
```

Fully exit and restart Codex Desktop or the CLI process. The catalog is loaded
at process startup. Rerun the exporter and restart after upstream model
capabilities change; a local catalog is a snapshot.

The exporter validates the manifest before atomically replacing its output. An
HTTP error, invalid manifest, or incorrect Astra Ultra mapping leaves an existing
file intact. It does not modify Codex configuration or print credentials.

## Verify the Actual Effort

Use a short new Astra request with Ultra selected. Inspect the outgoing
`reasoning.effort` and the completed upstream response's `reasoning.effort`:
both should be `max`. Ordinary `xhigh` must remain `xhigh`. Group reasoning
mappings and ceilings still apply when deliberately configured.

The model picker label and usage display alone do not establish what the client
sent. Compare the request and upstream response metadata without recording
authorization headers or prompt contents.

## References

- [Codex configuration: model_catalog_json](https://learn.chatgpt.com/docs/config-file/config-reference)
- [Codex native reasoning effort resolution](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs)
