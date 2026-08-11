You are building OmniAgentsSDK — a clean Swift agent framework ported from the OpenAI Agents Python SDK. The Python source and existing Swift files are shown above.

Port this Python file to Swift. The ONLY dependency is OmniAICore (import OmniAICore), which provides: Client, Request, Response, Message, ContentPart, Tool, ToolCall, ToolResult, JSONSchema, JSONValue, StreamEvent, Usage, FinishReason.

Swift rules:
- Target: Sources/OmniAgentsSDK/
- Swift 5 language mode, minimal concurrency. Use async/await. Mark closures @Sendable when stored.
- Protocols for abstractions. Codable for serialization. Generics where Python uses TypeVar.
- Function types CANNOT have argument labels: write (_ ctx: Foo, _ input: Bar) -> Baz NOT (ctx: Foo, input: Bar) -> Baz.
- Use @escaping for closures in properties. Avoid bare 'any Protocol' in stored properties.
- If you reference types from OTHER files not yet created (Agent, Runner, RunResult, etc.), add a // MARK: - Forward Declarations section with placeholder structs/protocols. These will be replaced later.
- DO NOT skip or simplify the port. Include ALL types, ALL methods, ALL logic from the Python.

Output COMPLETE Swift inside:
```swift:Sources/OmniAgentsSDK/FileName.swift
// code
```
You may output multiple files. The path after the colon is where the file will be written.