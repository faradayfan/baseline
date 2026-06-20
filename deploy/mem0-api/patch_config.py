#!/usr/bin/env python3
"""Rewrite mem0-api's hardcoded OpenAI DEFAULT_CONFIG to use Ollama.

The stock mem0/mem0-api-server image builds DEFAULT_CONFIG in /app/main.py with
`"provider": "openai"` for both the LLM and the embedder, and crashes at import
without OPENAI_API_KEY. It ignores any provider env vars. This build-time patch
replaces those two lines with Ollama provider blocks driven by env vars, so the
server runs fully self-hosted against our in-cluster Ollama (no vendor keys).

Replacement is anchored on the exact stock lines; if the upstream image changes
them, the build fails loudly (assert) rather than silently shipping OpenAI.
"""
import io
import sys

PATH = "/app/main.py"

OLD_LLM = '    "llm": {"provider": "openai", "config": {"api_key": OPENAI_API_KEY, "temperature": 0.2, "model": "gpt-4o"}},'
OLD_EMBED = '    "embedder": {"provider": "openai", "config": {"api_key": OPENAI_API_KEY, "model": "text-embedding-3-small"}},'

# Env-driven Ollama config. These env names are set by the Helm chart.
NEW_LLM = (
    '    "llm": {"provider": "ollama", "config": {'
    '"model": os.environ.get("OLLAMA_LLM_MODEL", "llama3.2:1b"), '
    '"ollama_base_url": os.environ.get("OLLAMA_BASE_URL", "http://ollama:11434"), '
    '"temperature": 0.2}},'
)
NEW_EMBED = (
    '    "embedder": {"provider": "ollama", "config": {'
    '"model": os.environ.get("OLLAMA_EMBEDDING_MODEL", "nomic-embed-text"), '
    '"ollama_base_url": os.environ.get("OLLAMA_BASE_URL", "http://ollama:11434"), '
    '"embedding_dims": int(os.environ.get("OLLAMA_EMBEDDING_DIMS", "768"))}},'
)

with io.open(PATH, "r", encoding="utf-8") as f:
    src = f.read()

assert OLD_LLM in src, "stock OpenAI llm line not found — upstream image changed; review patch_config.py"
assert OLD_EMBED in src, "stock OpenAI embedder line not found — upstream image changed; review patch_config.py"

src = src.replace(OLD_LLM, NEW_LLM).replace(OLD_EMBED, NEW_EMBED)

with io.open(PATH, "w", encoding="utf-8") as f:
    f.write(src)

print("patched /app/main.py: llm+embedder -> ollama provider", file=sys.stderr)
