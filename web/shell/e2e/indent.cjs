// Indentation and completion: Enter indents after `{` and `{{`, a closer
// outdents, and prose brings up no completion.
const { run, ready, lspConnected, assert } = require("./harness.cjs");

const value = (page) => page.evaluate(() => window.heronDebug.value());
const lastLines = async (page, n) => (await value(page)).split("\n").slice(-n);
const suggestVisible = (page) => page.$eval(".suggest-widget", (el) => el.classList.contains("visible")).catch(() => false);

run("indentation-and-completion", async ({ page, url }) => {
  await page.goto(url + "edit/specs/store/model.heron");
  await ready(page);
  await lspConnected(page);
  await page.click('[data-testid="editor"] .view-lines');
  await page.keyboard.press("Control+End");

  // `{` then Enter: the new line is indented and the closer outdented.
  await page.keyboard.press("Enter");
  await page.keyboard.type("notes: section {");
  await page.keyboard.press("Enter");
  let ls = await lastLines(page, 3);
  assert(ls[0] === "notes: section {" && ls[1] === "  " && ls[2] === "}", "after {: " + JSON.stringify(ls));

  // `{{` then Enter inside it.
  await page.keyboard.type("intro {{");
  await page.keyboard.press("Enter");
  ls = await lastLines(page, 5);
  assert(ls[1] === "  intro {{" && ls[2] === "    " && ls[3] === "  }}" && ls[4] === "}", "after {{: " + JSON.stringify(ls));

  // Prose brings up no completion, word by word.
  for (const word of ["Orders ", "are ", "kept ", "for ", "a ", "week."]) {
    await page.keyboard.type(word, { delay: 30 });
    await page.waitForTimeout(250);
    assert(!(await suggestVisible(page)), `completion popped up in prose after "${word}"`);
  }
  await page.keyboard.press("Escape");

  // A `|` line is prose too.
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  for (const word of ["| Each ", "order ", "has ", "an ", "owner"]) {
    await page.keyboard.type(word, { delay: 30 });
    await page.waitForTimeout(250);
    assert(!(await suggestVisible(page)), `completion popped up on a | line after "${word}"`);
  }

  // A closer typed on an indented line outdents it.
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  await page.keyboard.type("extra {");
  await page.keyboard.press("Delete");
  await page.keyboard.press("Enter");
  await page.keyboard.type("}");
  ls = await lastLines(page, 3);
  assert(ls[0] === "  extra {" && ls[1] === "  }", "a closer doesn't outdent: " + JSON.stringify(ls));
});
