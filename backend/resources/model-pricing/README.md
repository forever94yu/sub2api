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
