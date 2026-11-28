# Heron for VS Code

Heron in VS Code and the editors built on it, such as Cursor and VSCodium: the language
server's diagnostics, completion, hover, navigation, rename, formatting and fixes, instant
highlighting, and a live preview of the docs page of the node under the cursor.

The extension is a client of `heron lsp`, so it never disagrees with `heron check` and
`heron lint`. It needs the `heron` binary.

## Install

The extension is built as a `.vsix` file; it isn't on a marketplace.

1. Get `heron.vsix`: the `vscode` job of the repository's CI uploads it as the `heron-vsix`
   artifact, or build it yourself (below).
2. In VS Code run **Extensions: Install from VSIX…** and pick the file, or run
   `code --install-extension heron.vsix` (`cursor` and `codium` take the same flag).
3. Put `heron` on your `PATH`, or set `heron.path` to it.

To build it, from `editors/vscode`, with Node 22:

```sh
npm ci
npm run package   # writes heron.vsix
```

## Settings

| Setting      | Default | What it does                                                                 |
| ------------ | ------- | ---------------------------------------------------------------------------- |
| `heron.path` | empty   | The `heron` binary to run for the language server and the commands; empty runs `heron` from `PATH`. Changing it restarts the server. |

When the binary can't be found, the extension says so with a button that opens the setting,
and stays stopped until you run **Heron: Restart Language Server**.

## Features

- **Languages.** `heron` for `.heron` files and `heron-md` for `.heron.md` prose files, with
  Heron's TextMate grammars, so a file is coloured before the server answers; the Heron
  code in a `heron-md` file's `heron` fences is Heron. Line comments, brackets,
  auto-closing pairs and folding are configured for both.
- **Language server.** `heron lsp --stdio`, started in the first workspace folder:
  diagnostics with their fixes as code actions, completion, hover, go to definition,
  references, rename, formatting, document symbols, folding and semantic highlighting.
- **Live preview.** **Heron: Open Preview to the Side** (also the preview button in the
  editor's title bar) shows the node under the cursor as its docs page shows it, from the
  server's `heron/preview` request, and follows your edits and the cursor. A render that
  fails shows its error above the last page. The webview runs under a strict content
  security policy: no network, and only its own script and stylesheet.
- **Commands.** **Heron: Check**, **Heron: Lint**, **Heron: Generate Docs** and
  **Heron: Generate…** (pick the targets; none runs them all) run `heron` as a task in the
  terminal. **Heron: Restart Language Server** restarts the server.
- **VS Code for the Web.** The languages and grammars work in the browser; the commands that
  need the `heron` binary say they need the desktop editor.

To read the whole docs site live, run `heron serve` in the tree's folder and open the
address it prints; a reload shows your latest saved edits.

## Development

```sh
npm ci
npm run typecheck
npm test                                    # unit tests, no VS Code needed
HERON_BIN=/path/to/heron xvfb-run -a npm run test:e2e
```

`npm run test:e2e` downloads VS Code with `@vscode/test-electron`, opens a copy of
`test/fixture/tree` and checks diagnostics, hover, completion, the preview and a restart
against the real `heron lsp`. The grammars are copied from `grammar/textmate` at build time,
where `go run ./internal/tools/textmate/write` publishes them.
