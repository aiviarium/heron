// The shell's slots, for the frontend packages that register parts.
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
} from "./slots.ts";
export * as serverApi from "./api.gen.ts";
