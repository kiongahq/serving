# Changelog

## 0.1.0 (2026-11-11)


### ⚠ BREAKING CHANGES

* environment variables, headers and resource names are renamed.

### Features

* **auth:** require login for console access ([3bb016b](https://github.com/kiongahq/serving/commit/3bb016b84ea5958e25d433e7da6c87b6f8386ce8))
* coherent console, versioned pipeline DAGs and scheduling ([c8c5c87](https://github.com/kiongahq/serving/commit/c8c5c87a0e96e839b0fb9e0aa87b7cd013167f14))
* complete Kionga platform foundation ([e724dba](https://github.com/kiongahq/serving/commit/e724dbaf7623c95d4b82e96a644ca5af73aff6d2))
* **data-plane:** real feature store, object browsing, Langfuse, live catalog ([74bd4d2](https://github.com/kiongahq/serving/commit/74bd4d28a3cd8431bb3bafb260b685705c33da3c))
* **deploy:** add managed production packages and isolated workspaces ([a2aaf61](https://github.com/kiongahq/serving/commit/a2aaf6101721732b30d4f22e33b160e2eaa92d44))
* **editorial:** block content core with sanitized renderer, workflow, revisions, media pipeline and scheduled publisher ([7e4bd3e](https://github.com/kiongahq/serving/commit/7e4bd3ebfd3f1dad02fcf2be87a6db76e33f56c7))
* establish runnable MLOps platform foundation ([70f66b9](https://github.com/kiongahq/serving/commit/70f66b941c2b9aad5d84dd4793c99172550a6795))
* **execution:** real pipelines, model serving, serverless, and real-time processing ([f7f7fc0](https://github.com/kiongahq/serving/commit/f7f7fc0146d6d648ce52c6b23ee4ff07f0543974))
* **operator:** run leader-elected Kubernetes reconciliation manager ([57c22ef](https://github.com/kiongahq/serving/commit/57c22ef86bb2b219e4e0f483f9f5bff7e6324404))
* **operator:** run leader-elected Kubernetes reconciliation manager ([3f3ae86](https://github.com/kiongahq/serving/commit/3f3ae8675e75e01e5fd817d721d14b1ad48c2eaf))
* **release:** add reviewed versions and consistent publication ([ccf7d3b](https://github.com/kiongahq/serving/commit/ccf7d3be0db13b37de18605991df85d3c67966bf))
* **release:** standardize versions, changelogs and publication ([d6bd28e](https://github.com/kiongahq/serving/commit/d6bd28e0606ed65f88ea08f27b8c24dedb8cd42f))
* rename mlaiops to kionga ([#40](https://github.com/kiongahq/serving/issues/40)) ([c7b3126](https://github.com/kiongahq/serving/commit/c7b31261a504075e6ca8abee0712aae2168c3b5a))
* **security:** add OIDC verification and role-based access control ([b0ebe00](https://github.com/kiongahq/serving/commit/b0ebe00d9cc1da7be1d9a51c0dfc14ef5192fe62))
* **storage:** add PostgreSQL repository and transactional outbox ([f37c372](https://github.com/kiongahq/serving/commit/f37c372f3890679d8f2b253d598a2895f5b79c27))


### Fixes

* P1 serving security and readiness fixes ([#1](https://github.com/kiongahq/serving/issues/1)-[#5](https://github.com/kiongahq/serving/issues/5)) ([#37](https://github.com/kiongahq/serving/issues/37)) ([391f393](https://github.com/kiongahq/serving/commit/391f393c85e6fb5f28a6b1123723f485656d9f23))
* **release:** start unreleased components at version 0.1.0 ([24eb065](https://github.com/kiongahq/serving/commit/24eb0659f85c1eace6392b3194c5d1e0ed65fc18))
* **security:** Go 1.26.10 for crypto/x509 GO-2026-6809 ([d324774](https://github.com/kiongahq/serving/commit/d32477408df154452d68583e39814aed7198a21a))
* **security:** Go 1.26.11 for net/http GO-2026-6859 ([#41](https://github.com/kiongahq/serving/issues/41)) ([7b04f9a](https://github.com/kiongahq/serving/commit/7b04f9a8cc4135b617fa5fe3f9a5103cd60e33c6))
* **security:** golang.org/x/net v0.60.0 and Go 1.26.9 ([b9df4c3](https://github.com/kiongahq/serving/commit/b9df4c393552c3c2d8773cf5dc98530e1abf932f))
* **security:** internal service auth fails closed ([43faa89](https://github.com/kiongahq/serving/commit/43faa8930feabe714ffcab89145312de9653f9e5))
* **stack:** full-stack smoke green — 18/18 platform capabilities verified live ([986c061](https://github.com/kiongahq/serving/commit/986c0618b497466f81b2407fb9db1feccb864c46))
* **workspace:** make local IDE launch through current gateway ([b89ca4e](https://github.com/kiongahq/serving/commit/b89ca4ea948c87a9503029c8e6223a74ff4ff922))


### Documentation

* product README, CONTRIBUTING and ARCHITECTURE ([8597d98](https://github.com/kiongahq/serving/commit/8597d9831ccffd3e85a03f14a39e5d9589e9d72f))
* **release:** align contributor publication instructions ([24631b1](https://github.com/kiongahq/serving/commit/24631b1acc3c2ca6af78f5a28a3f4e4e139af4f2))
* **security:** clarify sole-maintainer release review ([ac46759](https://github.com/kiongahq/serving/commit/ac46759f49ed76cd4751e19308a63ce6aef43c66))


### Automation

* add pressure-test lane (full-stack + load + concurrency + vuln scan) ([e7099ad](https://github.com/kiongahq/serving/commit/e7099adbf7ad38c50da907e7324f8e4bb796fdbf))


### Maintenance

* **editorial:** vendor Editor.js and highlight.js with verified integrity manifest ([a799066](https://github.com/kiongahq/serving/commit/a799066101e15a3b2ac62e2d3929d41027cbfe47))
* ignore local tooling and secret files ([5c91172](https://github.com/kiongahq/serving/commit/5c91172d4a2589c9e55e67db81518fc08fd59281))
* license repository under MIT ([835e7fe](https://github.com/kiongahq/serving/commit/835e7fe0be7007d224ac1abe30020fe867209c61))
* **migrate:** move to the ml-ai-ops organization ([05a249c](https://github.com/kiongahq/serving/commit/05a249c79c156460debb3ec39fe2e306a401fb27))
* move to the kiongahq org; Go module github.com/kiongahq/platform, ghcr.io/kiongahq images, Kionga product name ([fbfe6d7](https://github.com/kiongahq/serving/commit/fbfe6d7095c7c240c4fa38e9fc6c474bfed19d83))
* remove the platform PRD document and all references to it ([7008316](https://github.com/kiongahq/serving/commit/7008316743ef9ab254f40f823c108673c6597046))
* standalone repo: own Go module, Makefile, CI and image publishing ([7aa69d6](https://github.com/kiongahq/serving/commit/7aa69d6441f435b8d9a6af4eb5da634762f0540b))

## Changelog

Reviewed release pull requests add versioned changes here.
Existing tags and releases remain available in [GitHub](https://github.com/kiongahq/serving/releases).
