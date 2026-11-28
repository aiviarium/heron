// The Heron extension on the desktop: the language client over `heron lsp`,
// the live preview and the commands that run `heron` in a task.
import * as vscode from "vscode";
import { LanguageClient, type LanguageClientOptions, type ServerOptions } from "vscode-languageclient/node";
import { cliCommands, generateArgs, generateTargets, heronCommand, notFoundMessage, resolveCommand, serverArgs } from "./core";
import { Preview, type PreviewState } from "./preview";

let client: LanguageClient | undefined;
let preview: Preview | undefined;

/** What activate returns, for the tests. */
export interface HeronApi {
  client(): LanguageClient | undefined;
  preview(): PreviewState | undefined;
  onPreview: vscode.Event<PreviewState>;
}

function folder(): vscode.WorkspaceFolder | undefined {
  return vscode.workspace.workspaceFolders?.[0];
}

function command(): string {
  return heronCommand(vscode.workspace.getConfiguration("heron").get<string>("path"));
}

async function notFound(cmd: string) {
  const open = "Open Settings";
  if ((await vscode.window.showErrorMessage(notFoundMessage(cmd), open)) === open) {
    await vscode.commands.executeCommand("workbench.action.openSettings", "heron.path");
  }
}

/** Starts the language client; when the command can't be started, says so and stays stopped. */
async function start(out: vscode.OutputChannel): Promise<void> {
  const cmd = command();
  const cwd = folder()?.uri.fsPath;
  const file = resolveCommand(cmd, process.env, cwd);
  if (!file) {
    void notFound(cmd);
    return;
  }
  const server: ServerOptions = { command: file, args: serverArgs, options: { cwd } };
  const options: LanguageClientOptions = {
    documentSelector: [
      { scheme: "file", language: "heron" },
      { scheme: "file", language: "heron-md" },
      { scheme: "untitled", language: "heron" },
      { scheme: "untitled", language: "heron-md" },
    ],
    outputChannel: out,
  };
  const c = new LanguageClient("heron", "Heron", server, options);
  try {
    await c.start();
    client = c;
  } catch (e) {
    out.appendLine(`heron lsp: ${e instanceof Error ? e.message : String(e)}`);
    void notFound(cmd);
  }
}

async function stop(): Promise<void> {
  const c = client;
  client = undefined;
  if (c) await c.stop().catch(() => undefined);
}

/** Runs `heron <args>` as a task in the first workspace folder's terminal. */
async function runTask(name: string, args: string[]) {
  const f = folder();
  if (!f) {
    void vscode.window.showErrorMessage("Heron: open a folder to run heron in.");
    return;
  }
  const cmd = command();
  const file = resolveCommand(cmd, process.env, f.uri.fsPath);
  if (!file) {
    void notFound(cmd);
    return;
  }
  const task = new vscode.Task({ type: "heron", args }, f, name, "heron", new vscode.ProcessExecution(file, args, { cwd: f.uri.fsPath }), []);
  task.presentationOptions = { reveal: vscode.TaskRevealKind.Always, clear: true };
  await vscode.tasks.executeTask(task);
}

export async function activate(ctx: vscode.ExtensionContext): Promise<HeronApi> {
  const out = vscode.window.createOutputChannel("Heron");
  preview = new Preview(() => client);
  ctx.subscriptions.push(out, preview);
  const restart = async () => {
    await stop();
    await start(out);
  };
  for (const [id, c] of Object.entries(cliCommands)) {
    ctx.subscriptions.push(vscode.commands.registerCommand(id, () => runTask(c.title, c.args)));
  }
  ctx.subscriptions.push(
    vscode.commands.registerCommand("heron.generate", async () => {
      const picked = await vscode.window.showQuickPick(generateTargets, {
        canPickMany: true,
        title: "Heron: Generate",
        placeHolder: "The targets to generate; none runs every target",
      });
      if (picked === undefined) return;
      await runTask(picked.length ? `generate ${picked.join(" ")}` : "generate", generateArgs(picked));
    }),
    vscode.commands.registerCommand("heron.openPreviewToSide", () => preview?.open(vscode.window.activeTextEditor)),
    vscode.commands.registerCommand("heron.restartServer", restart),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("heron.path")) void restart();
    }),
  );
  await start(out);
  return {
    client: () => client,
    preview: () => preview?.state,
    onPreview: preview.onShown.event,
  };
}

export async function deactivate(): Promise<void> {
  await stop();
}
