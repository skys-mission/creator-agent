// Package adapters is the model adapter layer: the only place in the project allowed to import
// provider SDKs. It turns a configured endpoint (Model) into a ModelClient speaking one wire
// protocol, and back-translates SDK output into contract events.
//
// Configuration keys on protocol, not vendor: a third-party endpoint that speaks the OpenAI
// Chat Completions dialect is ProtocolOpenAIChat no matter who runs it. Each protocol lives in
// its own subpackage so a provider SDK never leaks past that subpackage boundary.
//
// Protocol registry (implemented / reserved):
//
//	openai-chat-completions  adapters/openaichat   official openai-go SDK
//	anthropic-messages       (reserved)            official anthropic-sdk-go
//	openai-responses         (reserved)            official openai-go SDK
//
// Adding a protocol means adding one subpackage plus one case in New. The dependency direction
// is one-way: subpackages import contract and provider SDKs only, never this package.
package adapters
