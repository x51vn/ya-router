# Kilo Gateway Provider Specification

## Requirement: Deterministic Kilo routing

The system SHALL expose Kilo models with a `kilo/` prefix and SHALL route such requests only to `ProviderKilo`.

### Scenario: Auto Free routing

- **WHEN** a client requests `kilo/kilo-auto/free`
- **THEN** the router selects Kilo
- **AND** forwards `kilo-auto/free` upstream.

## Requirement: Credential isolation

The system SHALL use `KILO_API_KEY` before a configured Kilo key and SHALL NOT forward the inbound router authorization credential.

### Scenario: Authenticated request

- **WHEN** an inbound request contains the ya-router API key and `KILO_API_KEY` is configured
- **THEN** the upstream Authorization header contains only the Kilo key.

## Requirement: Anonymous requests are free-only

The system SHALL permit anonymous Kilo access only when configured and SHALL reject paid model IDs before upstream delivery.

### Scenario: Anonymous paid model

- **WHEN** anonymous mode is enabled without a Kilo API key
- **AND** a route resolves to a paid model
- **THEN** the provider returns an authentication error
- **AND** does not contact Kilo.

## Requirement: Transport fidelity

The system SHALL preserve Kilo HTTP status codes and SSE events for Chat Completions and native Responses requests.

### Scenario: Upstream rate limit

- **WHEN** Kilo returns HTTP `429`
- **THEN** ya-router returns HTTP `429` with the upstream error body.

### Scenario: Responses stream

- **WHEN** Kilo returns a Responses SSE stream
- **THEN** ya-router forwards the events without Chat Completions translation.

## Requirement: Data-handling warning

The system documentation SHALL state that Auto Free may use providers that log prompts or outputs or use them to improve services, and SHALL warn against confidential, personal, or regulated data.

