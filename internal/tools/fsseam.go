package tools

import "os"

// fsseam.go centralizes the three filesystem primitives used to move large
// JSON payloads (plan snapshots, commit manifests) out of the LLM's context
// and onto disk as file-path references. Tests substitute these package vars
// to simulate filesystem failures deterministically, without relying on
// platform-specific permission behavior.

// mkdirTempFunc matches os.MkdirTemp's signature.
var mkdirTempFunc = os.MkdirTemp

// writeFileFunc matches os.WriteFile's signature.
var writeFileFunc = os.WriteFile

// readFileFunc matches os.ReadFile's signature.
var readFileFunc = os.ReadFile
