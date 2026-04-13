// Package pipeline implements the core execution engine for multi-agent
// LLM workflows. Pipelines are directed graphs of nodes (agents, humans,
// tools, parallel fan-out) connected by conditional edges.
//
// # Pipeline Formats
//
// Pipelines can be defined in two formats:
//
//   - .dip (Dippin format) — the current format, parsed by dippin-lang.
//     Use FromDippinIR to convert parsed IR to a Graph.
//
//   - .dot (DOT/Graphviz format) — deprecated, will be removed in v1.0.
//     Use ParseDOT for backward compatibility only.
//
// New pipelines should use .dip format exclusively.
package pipeline
