// The shell's extension points, for later units' frontend packages.
export {
  registerPanel,
  registerGutterProvider,
  registerCommand,
  registerRenderer,
  allPanels,
  allGutterProviders,
  allCommands,
  gutterMarks,
  rendererFor,
  type Panel,
  type GutterProvider,
  type GutterMark,
  type GutterContext,
  type Command,
  type KindRenderer,
} from "./extensions.ts";
export * as serverApi from "./api.gen.ts";
