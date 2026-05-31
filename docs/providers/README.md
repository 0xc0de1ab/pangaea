# Provider Design Notes

이 디렉토리는 Pangaea monorepo LLM runtime platform에서 지원할 provider별
동작 상세를 기록한다.

각 문서는 현재 구현 완료 상태가 아니라 provider 설계와 구현 기준이다.

## Provider Kinds

- [CLI Container Providers](./cli-container-provider.md)
- [API-Compatible Providers](./api-compatible-provider.md)
- [Sidecar Providers](./sidecar-provider.md)
- [Setup Provider Command](./setup-provider.md)

## CLI Providers

- [Codex CLI Provider](./codex-cli-provider.md)
- [Claude CLI Provider](./claude-cli-provider.md)
- [Gemini CLI Provider](./gemini-cli-provider.md)
- [Gemini ACP JSON-RPC Notes](./gemini-acp-rpc.md)
- [Grok Build Provider](./grok-build-provider.md)

## API Providers

- [GLM API Provider](./glm-api-provider.md)
- [MiniMAX API Provider](./minimax-api-provider.md)
- [DeepSeek API Provider](./deepseek-api-provider.md)

## Sidecar Providers

- [Antigravity Sidecar Provider](./antigravity-sidecar-provider.md)
- [Cline Sidecar Provider](./cline-sidecar-provider.md)
- [GitHub Copilot Sidecar Provider](./github-copilot-sidecar-provider.md)

## Document Template

Provider docs should use this section order:

- Purpose
- Kind
- Capabilities
- Auth
- Bootstrap
- Refresh
- Runtime / Local Server
- Models
- Usage
- Routing Notes
- Limitations
- Tests
