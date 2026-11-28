// The Heron extension in a browser, as VS Code for the Web runs it: the
// languages and grammars come from package.json; the commands that need
// the `heron` binary say they need the desktop editor.
import * as vscode from "vscode";

const needsDesktop = ["heron.check", "heron.lint", "heron.generateDocs", "heron.generate", "heron.openPreviewToSide", "heron.restartServer"];

export function activate(ctx: vscode.ExtensionContext) {
  for (const id of needsDesktop) {
    ctx.subscriptions.push(
      vscode.commands.registerCommand(id, () =>
        vscode.window.showInformationMessage("Heron: this command runs the heron binary, which needs the desktop editor."),
      ),
    );
  }
}

export function deactivate() {}
