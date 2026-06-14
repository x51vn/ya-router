## 1. Request Normalization

- [x] 1.1 Extend Responses request modeling to capture `store` and normalize Codex native responses requests to `store: false`
- [x] 1.2 Update the native Codex request builder so marshaled upstream JSON always includes `store: false`

## 2. Regression Coverage

- [x] 2.1 Add adapter tests asserting native Codex responses bodies include `store: false` for omitted and explicit `store=true` client inputs
- [x] 2.2 Add routed proxy/provider tests asserting Codex `/v1/responses` dispatch sends `store: false` to the provider

## 3. Validation

- [ ] 3.1 Run `make fmt && make vet && make test && make build`
- [ ] 3.2 Deploy with the existing `server2` workflow and verify the upstream `Store must be set to false` error is resolved
