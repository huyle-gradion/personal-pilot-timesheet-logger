// Package mcp implements a generic MCP (Model Context Protocol) JSON-RPC 2.0
// transport over HTTP POST. It is deliberately narrow and hermetic-friendly:
// stdlib only, no REST fallback, no retries, and fail-closed framing.
//
// Secret handling: the bearer token lives only in the Authorization header and
// in Session memory. It is never logged, never placed in an error, and never
// serialized. Error messages are bounded and run through a literal redactor as
// defense in depth.
package mcp

import "time"

// Named finite bounds with explicit units (TigerStyle).
const (
	// DefaultRequestTimeout bounds a single MCP HTTP request.
	DefaultRequestTimeout = 15 * time.Second
	// DefaultMaxResponseBytes bounds one MCP response body (256 KiB).
	DefaultMaxResponseBytes int64 = 256 * 1024
	// MaxToolsCount caps the number of tools admitted from tools/list.
	MaxToolsCount = 200
	// MaxTextBlockBytes bounds the first text block extracted from a tool result.
	MaxTextBlockBytes = 64 * 1024
	// maxErrorSnippetBytes bounds the response-body preview embedded in an HTTP
	// status error. Redaction runs over the full body before truncation.
	maxErrorSnippetBytes = 256
)

// ProtocolVersion is the single MCP protocol version this transport speaks.
const ProtocolVersion = "2025-06-18"

// DefaultClientName / DefaultClientVersion identify this client during initialize.
const (
	DefaultClientName    = "pilot-timesheet"
	DefaultClientVersion = "1.0.0"
)
