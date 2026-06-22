# Changelog

## [0.2.1](https://github.com/faradayfan/baseline/compare/v0.2.0...v0.2.1) (2026-06-22)


### Bug Fixes

* **contextsvc:** degrade to facts-only when the memory backend is unreachable ([428c619](https://github.com/faradayfan/baseline/commit/428c619e3f160a7ca2f7e08d6c1cb6aa835d0de0))
* **frontend:** upgrade Node 22 → 26 ([c443e2f](https://github.com/faradayfan/baseline/commit/c443e2f644ecd697254b5da681574233897c6ba7))

## [0.2.0](https://github.com/faradayfan/baseline/compare/v0.1.0...v0.2.0) (2026-06-21)


### Features

* adjusted mcp sever settings ([5c5ecdf](https://github.com/faradayfan/baseline/commit/5c5ecdfe6264bd8c0f0e4e8e9fdea1de63c8a603))
* **autopromote:** add pluggable versioned auto-promote engine (M2a) ([ce5c6f3](https://github.com/faradayfan/baseline/commit/ce5c6f35716de028a37586b3d5727faecb07d10c))
* **cli:** add baseline-mcp helper for driving the MCP tools from a shell ([b9f1f42](https://github.com/faradayfan/baseline/commit/b9f1f42f4694fb403cd3f8bbe80bfa1f2c40d6df))
* **context,facts:** tag filtering on the read path ([bc297b6](https://github.com/faradayfan/baseline/commit/bc297b68f2711c781f2e09e810b2e7d3356ec15c))
* **context,memory:** add /context resolver and MemorySource adapters (M3) ([7a4005d](https://github.com/faradayfan/baseline/commit/7a4005d7d86735cfbb9290489844bfaa1bc46be9))
* **deploy,mem0:** wire Mem0 on the Pi cluster + OSS adapter (M7-POC Phase 2) ([5daddf9](https://github.com/faradayfan/baseline/commit/5daddf9165291f86c426227c876f25fab9b00f6e))
* **deploy:** remote MCP-over-HTTP + Pi cluster Helm chart (M7-POC phase 1) ([0a5ae38](https://github.com/faradayfan/baseline/commit/0a5ae3817ad2b8e2afe07bf2cc5a6b1a109b40a8))
* **deploy:** run the full stack locally on Docker Desktop Kubernetes ([f239af7](https://github.com/faradayfan/baseline/commit/f239af78bad9281632a22ef085cad4e028b6c62f))
* **facts,promotions:** add fact lifecycle, audit, and promotion workflow (M2) ([3cf018d](https://github.com/faradayfan/baseline/commit/3cf018db42a2dd4c8092629414c0fe9cd8eae27b))
* **facts:** semantic (embedding-ranked) fact search ([432a81a](https://github.com/faradayfan/baseline/commit/432a81ac5667da632bf21048a572010a4f8ec6b5))
* **frontend:** read-only dashboard for facts, promotions, audit, and context ([afcde64](https://github.com/faradayfan/baseline/commit/afcde6403455483ad06cb2ad958be0c4bc38b439))
* **frontend:** show memory cognitive type in the context preview ([82997d2](https://github.com/faradayfan/baseline/commit/82997d2ab759f8b4cd118f0fe4fc1d339d809d5f))
* **helm:** wire Baseline's own fact embedder (EMBEDDER_URL/MODEL/DIMS) ([b2da8ee](https://github.com/faradayfan/baseline/commit/b2da8ee17ffad8dbe309a2a76e31accfd3b48824))
* **mcp:** accept tags on the propose_fact tool ([ae2f269](https://github.com/faradayfan/baseline/commit/ae2f2690d30b325e39c0c5e240db21efa9208a28))
* **mcp:** add list_namespaces + submit_promotion tools; complete the authoring flow ([71c8013](https://github.com/faradayfan/baseline/commit/71c8013d0fefb8263b53c4fe0a630e3bd33d9bae))
* **mcp:** add MCP bridge exposing the §9 tools over REST (M4) ([ad08084](https://github.com/faradayfan/baseline/commit/ad0808498421516db19754410ebc1724a9ac1882))
* **mcp:** add save_memory tool for native, structured memory capture ([a6da815](https://github.com/faradayfan/baseline/commit/a6da81561ca7e71b67f8370cedc9cbe91d4b36d0))
* **memory,hooks:** out-of-band memory capture — harness → Mem0 via Baseline ([9178698](https://github.com/faradayfan/baseline/commit/9178698c9167bd301fd75467a71795e79d4b7cb8))
* **memory,mem0:** verbatim capture via infer=false for [remember:] ([fd656e8](https://github.com/faradayfan/baseline/commit/fd656e8dc2ce65c04bb156cc5177485074209e86))
* **plugin,context:** tiered fact injection to keep agent context lean ([92ed3ac](https://github.com/faradayfan/baseline/commit/92ed3ac58fecc72b14d4a36f8ac80477c080a5b3))
* **plugin:** package the Claude Code integration as an installable plugin ([b5ba3d3](https://github.com/faradayfan/baseline/commit/b5ba3d3af2e64332331e138fc98dc3e8b6f899dc))
* **plugin:** teach the capture convention at SessionStart (opt-in, capture-sparingly) ([055a316](https://github.com/faradayfan/baseline/commit/055a31632a09ae6f4060e1bb1f1b78a09475f8f2))
* **plugin:** typed memory capture ([remember:TYPE:]) + fix Mem0 dim bug ([d7495fb](https://github.com/faradayfan/baseline/commit/d7495fb0add3df8df9b3a16fea5acb8ba2a93ca0))
* **rbac:** add RBAC, entitlement resolution, and auth middleware (M1) ([cba9275](https://github.com/faradayfan/baseline/commit/cba9275a3c05e282f458937aea5445e6df911969))
* **reaper,otel:** add staleness reaper and OTEL observability (M5) ([0ebe293](https://github.com/faradayfan/baseline/commit/0ebe293f0adb79b53b2af57309805c1413a4452c))
* scaffold Baseline service with M0 schema, store, and namespaces ([c49f1c5](https://github.com/faradayfan/baseline/commit/c49f1c50a54523faab931912d832472edcf802a1))
* **seed:** reproduce tiered facts so a fresh seed matches the plugin model ([7ffd2e7](https://github.com/faradayfan/baseline/commit/7ffd2e70729d736b1c7fa8c5dbbc11794159c35b))


### Bug Fixes

* **ci:** correct lint/govulncheck/mem0-build pipeline failures ([33a0b88](https://github.com/faradayfan/baseline/commit/33a0b88e4b78a3759337136afdf407429e1e8611))
* **ci:** pin pnpm 11 and register bitnami repo for helm deps ([41ee9b6](https://github.com/faradayfan/baseline/commit/41ee9b628b3ea8aa545d2adca3d1dbd5f0ee6c1a))
* **ci:** scope pnpm to frontend/ and build helm chart deps ([655e05c](https://github.com/faradayfan/baseline/commit/655e05c7e971c996d03e1444412e3ae891eb01d1))
* **cli:** parse flags after the tool name in baseline-mcp ([a25f53c](https://github.com/faradayfan/baseline/commit/a25f53c99de903aecdaaa3b9b2f08483ff922f4c))
* **deploy:** authenticate seed.sh via the Bitnami postgres secret ([5e0dfe9](https://github.com/faradayfan/baseline/commit/5e0dfe9877c0d145678ab3f080669c9b38ef8415))
* **deps-dev:** bump the frontend-dev group in /frontend with 3 updates ([dacedad](https://github.com/faradayfan/baseline/commit/dacedad3525da2344ac9ee2de421c09fd0355767))
* **deps-dev:** bump the frontend-dev group in /frontend with 3 updates ([cbfb888](https://github.com/faradayfan/baseline/commit/cbfb888b84100daf9a44794f73666d7001f30b27))
* **deps:** bump golang.org/x/crypto to v0.52.0 to clear High/Critical CVEs ([434d467](https://github.com/faradayfan/baseline/commit/434d46746c830c6469c807685b89bb8f72c0e5a7))
* **deps:** bump the github-actions group with 2 updates ([7865d74](https://github.com/faradayfan/baseline/commit/7865d748edb3740533118ff4d9fbb091552b7c4e))
* **deps:** bump the github-actions group with 2 updates ([3163865](https://github.com/faradayfan/baseline/commit/3163865b90bbe69605967b73c177c08f77b256dd))
* **deps:** bump the go-modules group with 4 updates ([28240c2](https://github.com/faradayfan/baseline/commit/28240c20687f4c7375e72c96324334b5c6ee1c68))
* **deps:** bump the go-modules group with 4 updates ([61bc85d](https://github.com/faradayfan/baseline/commit/61bc85d8f06a1e0234675cb8c053520a11462adc))
* **docs:** escape semicolons in mermaid sequence messages ([7b69364](https://github.com/faradayfan/baseline/commit/7b6936470cfa526a5840920e883539c5f8dfd381))
* **frontend:** bump nginx base to clear High/Critical CVEs ([5125f3a](https://github.com/faradayfan/baseline/commit/5125f3ac3081c8d6121b88caf714e0d75105666c))
* **frontend:** copy pnpm-workspace.yaml in Docker build so install succeeds ([987188f](https://github.com/faradayfan/baseline/commit/987188f549fb3470d3fe96afff0bd5e664216f34))
* **helm,make:** force pod rollout on local deploy via rollme annotation ([acdc544](https://github.com/faradayfan/baseline/commit/acdc544b1c71007a4b98dab817a654707fe4cb8b))
* **mcp:** populate structuredContent in tool results ([0c19e66](https://github.com/faradayfan/baseline/commit/0c19e666081716ae8c04201bc15a0deb62b920f5))
* **plugin:** capture [remember:] from the whole turn, not just the last message ([bf5a27d](https://github.com/faradayfan/baseline/commit/bf5a27d5dac2068304dff07d68af3041deb51db9))
* **plugin:** make install resilient when the userConfig prompt doesn't fire ([0cd2c72](https://github.com/faradayfan/baseline/commit/0cd2c727fd7cc696dae2137a3eda0b30efb38dc2))
* **plugin:** only capture genuine [remember:TYPE:] markers from the current turn ([585c0b7](https://github.com/faradayfan/baseline/commit/585c0b7d98991bbeed6af8eef21c84ddadc4f9c5))
* **plugin:** track commit SHA for updates + durable config across updates ([4774482](https://github.com/faradayfan/baseline/commit/47744824243301f9e7e71603e05c28aced92a990))
