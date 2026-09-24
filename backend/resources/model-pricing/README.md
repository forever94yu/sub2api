# Model Pricing Data

This directory contains a local copy of the mirrored model pricing data as a fallback mechanism.

## Source
The original file is maintained by the LiteLLM project and mirrored into the `price-mirror` branch of this repository via GitHub Actions:
- Mirror branch (configurable via `PRICE_MIRROR_REPO`): https://raw.githubusercontent.com/<your-repo>/price-mirror/model_prices_and_context_window.json
- Upstream source: https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json

## Purpose
This local copy serves as a fallback when the remote file cannot be downloaded due to:
- Network restrictions
- Firewall rules
- DNS resolution issues
- GitHub being blocked in certain regions
- Docker container network limitations

## Update Process
The pricingService will:
1. First attempt to download the latest version from GitHub
2. If download fails, use this local copy as fallback
3. Log a warning when using the fallback file

## Manual Update
To manually update this file with the latest pricing data (if automation is unavailable):
```bash
curl -s https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json -o model_prices_and_context_window.json
```

## File Format
The file contains JSON data with model pricing information including:
- Model names and identifiers
- Input/output token costs
- Context window sizes
- Model capabilities

Last updated: 2025-08-10

## Claude Pricing Verification (2026-09-24)

Claude entries and offline fallback rates were checked against
[Anthropic's official pricing](https://platform.claude.com/docs/en/about-claude/pricing)
and Wei-Shaw/sub2api commit `a3eb7ef302961cba716dc78b39b93b60c467db0e`.
The official prices take precedence over stale values in the reference mirror.

Prices below are USD per million tokens. Five-minute writes cost 1.25 times
input pricing; one-hour writes cost twice input pricing.

| Model | Input | Output | Cache Read |
| --- | ---: | ---: | ---: |
| Fable 5 | 10 | 50 | 1 |
| Fable 5.1 | 10 | 50 | 0.25 |
| Opus 5.5 | 4 | 20 | 0.20 |
| Opus 5 / 4.8 / 4.7 / 4.6 / 4.5 | 5 | 25 | 0.50 |
| Opus 4.1 / 4 / 3 | 15 | 75 | 1.50 |
| Sonnet 5 | 2 | 10 | 0.20 |
| Sonnet 4.6 / 4.5 / 4 / 3.7 / 3.5 | 3 | 15 | 0.30 |
| Haiku 4.5 | 1 | 5 | 0.10 |
| Haiku 3.5 | 0.80 | 4 | 0.08 |
| Haiku 3 | 0.25 | 1.25 | 0.025 |

Sonnet 4 and 4.5 use the existing documented >200k prompt tier (2x input/cache,
1.5x output). Claude 4.6 and newer retain standard rates across their context
window. First-party Fast costs 2x for Opus 4.8, Opus 5, and Opus 5.5; US-only
inference costs 1.1x for supported 4.6+ models. These modifiers stack, and
response declarations can lower a requested premium but cannot raise it.
Partner endpoint prices may be configured explicitly and are resolved before
native aliases. Unknown Claude versions require explicit pricing.

## GPT-6 Sol and Luna

The following entries were verified against the official OpenAI documentation on
2026-09-23. Adding them does not update any existing model's prices.

| Model | Input | Cached input | Cache writes | Output |
| --- | ---: | ---: | ---: | ---: |
| `gpt-6-sol` | $2.00 | $0.20 | $2.50 | $10.00 |
| `gpt-6-luna` | $0.10 | $0.01 | $0.125 | $0.50 |

Prices are USD per million tokens. Fast (`service_tier: priority` or `fast`)
uses twice the standard rates; Flex uses half. Billing uses the tier reported by
the upstream response, falling back to the forwarded request tier when absent.
Input includes both cache reads and cache writes for the long-context threshold.
Above 272,000 input tokens, the full request uses 2x input/cache rates and 1.5x
output rates, subject to the existing account and group long-context settings.
Reasoning tokens are already included in output usage and are not charged twice.

Both official IDs support a 1,050,000-token context window, up to 922,000 input
tokens and 128,000 output tokens. Their reasoning efforts are `none`, `low`,
`medium` (default), `high`, `xhigh`, and `max`. Use Responses for tool calls with
reasoning; native Chat Completions function calling requires `none`.

Sources: [Sol](https://developers.openai.com/api/docs/models/gpt-6-sol),
[Luna](https://developers.openai.com/api/docs/models/gpt-6-luna),
[pricing](https://developers.openai.com/api/docs/pricing), and
[GPT-6 API compatibility](https://developers.openai.com/api/docs/guides/latest-model).
