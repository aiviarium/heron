import { render } from "@solidjs/web";
import "@heron/components/components.css";
import "./app.css";
import { App } from "./App.tsx";
import { registerBuiltins } from "./builtins.tsx";
import { start } from "./state.ts";

registerBuiltins();
render(() => <App />, document.getElementById("app")!);
void start();
