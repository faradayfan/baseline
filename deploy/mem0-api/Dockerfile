# Custom mem0-api image. The stock mem0/mem0-api-server image ships only the
# dependencies for its default OpenAI config and is missing the libraries its OWN
# pgvector vector store + neo4j graph store need (psycopg, neo4j drivers) — so it
# crashes at startup regardless of LLM provider. It also tries to pip-install some
# of these via an interactive prompt that crashes EOFError in a non-TTY. This
# image installs them ahead of time.
#
# PATCH_OLLAMA=1 additionally rewrites DEFAULT_CONFIG to use the Ollama provider
# (fully self-hosted, no vendor keys). Default (0) keeps the stock OpenAI config
# — used because Pi-CPU Ollama is too slow/weak for Mem0's JSON extraction.
#
# Build (arm64 for the Pi registry):
#   docker buildx build --platform linux/arm64 \
#     -t <REGISTRY_HOST>:5000/baseline-mem0-api:openai --push deploy/mem0-api
#   # or, self-hosted: --build-arg PATCH_OLLAMA=1 -t ...:ollama

ARG MEM0_IMAGE=mem0/mem0-api-server:latest
FROM ${MEM0_IMAGE}

# Vector/graph store drivers the stock image lacks (needed on every path).
RUN pip install --no-cache-dir "psycopg[binary,pool]" "psycopg2-binary" "neo4j" "langchain-neo4j" "rank-bm25" "ollama"

ARG PATCH_OLLAMA=0
COPY patch_config.py /tmp/patch_config.py
RUN if [ "$PATCH_OLLAMA" = "1" ]; then python3 /tmp/patch_config.py; fi && rm /tmp/patch_config.py
