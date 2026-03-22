# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Variable Interpolation**: Full support for `${namespace.key}` syntax in prompts and node attributes
  - `${ctx.*}` namespace for runtime pipeline context (outcome, last_response, human_response, etc.)
  - `${params.*}` namespace for subgraph parameters
  - `${graph.*}` namespace for workflow-level attributes (goal, name, etc.)
  - Lenient mode by default (undefined variables expand to empty string)
  - Strict mode available for development/debugging
  - Complete integration with codergen handler and subgraph parameter passing
  - Comprehensive test coverage (>95%)
  - Example workflows demonstrating all three namespaces

### Changed
- Codergen handler now uses `ExpandVariables()` for template expansion
- Subgraph handler now injects params into child graphs before execution

### Technical Details
- Added `pipeline/expand.go` with core expansion logic
- Added `pipeline/expand_test.go` with comprehensive unit tests
- Added `pipeline/handlers/expand_integration_test.go` for end-to-end testing
- Added test fixtures in `testdata/expand_*.dip`
- Updated README.md with variable interpolation documentation
- Added example workflows: `examples/variable_interpolation_demo.dip`, `examples/variable_interpolation_child.dip`

## [Previous Versions]
(No prior changelog entries - this is the initial CHANGELOG)
