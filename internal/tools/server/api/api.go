// Package api is the server's API, generated from server.heron by apigen:
// the types, the Service the server implements and the routes that bind
// each op to it. The TS client is generated beside it, in web/shell.
package api

//go:generate go run ../apigen -base server.heron -go api_gen.go -pkg api -ts ../../../../web/shell/src/api.gen.ts
