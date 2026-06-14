## 1. Symbol Audit

- [x] 1.1 List every unexported symbol (functions, types, constants, variables) referenced in `src/*_test.go` that currently comes from non-test `src/*.go` files — produce the definitive map of `symbol → internal package`.

## 2. Stub Files Creation

- [x] 2.1 Create `src/stubs_config.go`: type aliases and wrapper functions for config-related symbols (`Config`, `defaultConfig`, `loadConfig`, `saveConfig`, `loadDefaultConfigFromExample`, `applyConfigDefaults`, `mergeConfigs`, `migrateConfig`, `ensureCodexModelMap`, `setDefaultTimeouts`, types: `CopilotProviderConfig`, `CopilotAccount`, `CopilotAuthState`, `CodexAccount`, `CodexAuthState`, `ModelMapEntry`, `RoutingConfig`, constants: `currentConfigVersion`).
- [x] 2.2 Create `src/stubs_auth.go`: type aliases and wrapper functions for auth symbols (`codexAuthenticate`, `codexRefreshToken`, `copilotAuthenticate`, `copilotRefreshToken`, `isChatGPTMode`, `resolveCodexAPIKey`, `resolveCodexChatGPTAuth`, `applyResolvedCodexChatGPTAuth`, `clearPersistedChatGPTSecrets`, `writeOfficialAuthJSON`, `loadOfficialCodexModels`, `extractAccountIDFromJWT`, `isUnrecoverableRefreshError`, constants: `codexCredentialSourceEnv`, `codexCredentialSourceOfficialStore`, `codexCredentialSourceProxyConfig`, type: `resolvedCodexChatGPTAuth`).
- [x] 2.3 Create `src/stubs_provider.go`: type aliases and wrapper functions for provider and model symbols (`ProviderID`, `ProviderCopilot`, `ProviderCodex`, `Capability`, `CapabilityChat`, `CapabilityEmbeddings`, `CapabilityResponses`, `ProviderHealth`, `Provider`, `ProviderRegistry`, `NewProviderRegistry`, `ModelList`, `Model`, `ModelCache`, `ModelRouter`, `NewModelRouter`, `modelsHandler`, `filterAllowedModels`, `isModelAllowed`, `knownModelList`, `intersectNormalizedModelNames`, `cloneModelList`).
- [x] 2.4 Create `src/stubs_proxy.go`: wrapper functions for proxy/router/server symbols (`processProxyRequest`, `capabilityFromPath`, `healthHandler`, `makeRequestWithRetry`, `makeRequest`, `getRequestKey`, `setDefaultTimeouts`, `CircuitBreaker`, `canExecute`, `onSuccess`, `onFailure`, `CoalescingCache`, `WorkerPool`).
- [x] 2.5 Create `src/stubs_providers.go`: type aliases and wrappers for concrete provider structs (`CopilotProvider`, `NewCopilotProvider`, `CodexProvider`, `NewCodexProvider`, `CopilotFreeCatalog`, `normalizeCopilotAccounts`, `normalizeCodexAccounts`, `advanceAccount`, `firstHealthyAccount`, `isAccountLimitSignal`, `advanceCodexAccount`, `firstHealthyCodexAccount`, `isCodexAccountInCooldown`, `activeAccount`, `buildTestConfigWithAccounts`, `buildTestCodexConfigWithAccounts`).
- [x] 2.6 Create `src/stubs_transform.go`: wrappers for transform/adapter symbols (`extractModelFromBody`, `patchBodyModel`, `buildChatCompletionsRequestFromResponses`, `buildChatGPTCodexRequest`, `buildChatGPTCodexRequestFromResponses`, `buildPlatformResponsesRequest`, `isStreamingRequest`, `responsesToChatCompletion`, `chatCompletionToResponses`, `chatCompletionStreamToResponses`, `normalizeContentParts`, `normalizeEmbeddingsRequestBody`, `ensureEmbeddingsResponseCompat`, `convertToolsForChat`, `convertToolsForResponses`, `streamOptionsIncludeUsage`, `streamResponsesAsChat`, `extractMessages`, `applyResponsesReasoningToChat`).
- [x] 2.7 Create `src/stubs_prefixes.go`: wrappers for prefix-related symbols (`AddModelPrefix`, `resolveEffectiveCopilotFreeModels`, `parseCopilotPlanFreeModels`, `parseZeroPremiumModels`, `cellHasIncludedMarker`).
- [x] 2.8 Create `src/stubs_catalog.go`: wrappers for free catalog symbols (`CopilotFreeCatalog` ← already in stubs_providers, but also `freeChatProxyFunc`, `responsesProxyFunc`, `proxyFunc`).
- [x] 2.9 Create `src/stubs_misc.go`: any remaining symbols not covered (`loadDefaultConfigFromExample`, `codexHomePath`, `chownToServiceUser`, etc.) that tests reference — leave out any that no test actually calls.

## 3. Delete Production Files

- [x] 3.1 Delete `src/auth.go`, `src/codex_auth.go`, `src/config.go`, `src/models.go`, `src/prefixes.go`, `src/provider.go`, `src/registry.go`.
- [x] 3.2 Delete `src/cli.go`, `src/main.go`, `src/server.go`.
- [x] 3.3 Delete `src/proxy.go`, `src/router.go`, `src/embeddings.go`.
- [x] 3.4 Delete `src/transform.go`, `src/responses_adapter.go`, `src/model_cache.go`.
- [x] 3.5 Delete `src/copilot_provider.go`, `src/codex_provider.go`, `src/copilot_free_catalog.go`.

## 4. Verification

- [x] 4.1 Run `go build ./src/...` (must succeed — stubs + tests compile together as `package main`).
- [x] 4.2 Run `make fmt && make vet && make test && make build` — all must pass.
- [x] 4.3 Spot-check: `./github-copilot-svcs --help` prints usage from the new binary.
