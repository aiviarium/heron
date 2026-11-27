module github.com/aiviarium/heron

go 1.24.7

require github.com/fxamacker/cbor/v2 v2.9.6

require (
	github.com/aiviarium/sparrow-rpc/go v0.0.0
	github.com/coder/websocket v1.8.14
	github.com/x448/float16 v0.8.4 // indirect
)

replace github.com/aiviarium/sparrow-rpc/go => ../sparrow/go
