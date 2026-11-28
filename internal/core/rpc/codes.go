package rpc

import (
	"strconv"
	"strings"
)

// The codes an error's @status derives on the surfaces that don't speak
// HTTP, as the Specifications tab's derived codes say: its gRPC code, its
// Sparrow core status code, its JSON-RPC code and its exit status. An
// override annotation of a surface sets its code instead.

// grpcByStatus is the standard mapping of an HTTP status to a gRPC code.
var grpcByStatus = map[int]string{
	400: "INVALID_ARGUMENT",
	401: "UNAUTHENTICATED",
	403: "PERMISSION_DENIED",
	404: "NOT_FOUND",
	409: "ALREADY_EXISTS",
	429: "RESOURCE_EXHAUSTED",
	499: "CANCELLED",
	500: "INTERNAL",
	501: "UNIMPLEMENTED",
	503: "UNAVAILABLE",
	504: "DEADLINE_EXCEEDED",
}

// statusCode reads a status as written, as "404"; false when it isn't one.
func statusCode(status string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(status))
	return v, err == nil
}

// GRPCCode is the gRPC code an HTTP status derives, as NOT_FOUND for
// "404": a listed status by the table, any other 4xx FAILED_PRECONDITION,
// any other 5xx INTERNAL and anything else UNKNOWN; "" for no status.
func GRPCCode(status string) string {
	v, ok := statusCode(status)
	if !ok {
		return ""
	}
	if c, ok := grpcByStatus[v]; ok {
		return c
	}
	switch {
	case v >= 400 && v < 500:
		return "FAILED_PRECONDITION"
	case v >= 500 && v < 600:
		return "INTERNAL"
	}
	return "UNKNOWN"
}

// SparrowCodeName is the Sparrow core status code an HTTP status derives:
// the gRPC code's name in lower case, as not_found; "" for no status.
func SparrowCodeName(status string) string {
	return strings.ToLower(GRPCCode(status))
}

// JSONRPCCode is the JSON-RPC code an HTTP status derives: -32000 − (status
// − 400) for a 4xx, in the server range; -32601, method not found, for 501;
// and -32603, internal error, for anything else. False for no status.
func JSONRPCCode(status string) (int, bool) {
	v, ok := statusCode(status)
	if !ok {
		return 0, false
	}
	switch {
	case v >= 400 && v < 500:
		return -32000 - (v - 400), true
	case v == 501:
		return -32601, true
	}
	return -32603, true
}

// ExitCode is the exit status an HTTP status derives: 2, a usage error,
// for 400, and 1, a failure, for anything else. False for no status.
func ExitCode(status string) (int, bool) {
	v, ok := statusCode(status)
	if !ok {
		return 0, false
	}
	if v == 400 {
		return 2, true
	}
	return 1, true
}
