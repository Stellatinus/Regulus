// The widget of the example plugin (components.ui.module in plugin.yaml).
//
// NOTHING LOADS THIS FILE YET. Regulus checks that it is named correctly and
// is inside the plugin, and that is all: loading widgets at runtime, the API
// they are given and their isolation are the design of #21. This file shows
// where a widget lives and has the shape the design proposes (an ES module
// that registers widgets). Expect to change it when #21 is decided.

export function register(host) {
  host.addWidget({
    id: "example-hello.greeting",
    title: "Hello",
    // permission an administrator must have given to see it (plugin.yaml)
    permission: "example-hello.read",
    render(container) {
      container.textContent = "Hello from the example plugin.";
    },
  });
}
