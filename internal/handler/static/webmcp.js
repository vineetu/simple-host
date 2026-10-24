'use strict';
// Tiny helper for document.modelContext.registerTool (WebMCP). No other script
// is required. A browser that does not implement the draft, or implements it
// halfway, must not break the page: supported() is false, and registerTool
// swallows errors. navigator.modelContext is the deprecated fallback (Chrome 150+).
window.SHWebMCP = {
  supported: function () {
    return typeof document.modelContext?.registerTool === 'function' ||
      typeof navigator.modelContext?.registerTool === 'function';
  },
  registerTool: function (spec) {
    try {
      if (typeof document.modelContext?.registerTool === 'function') {
        document.modelContext.registerTool(spec);
        return;
      }
      if (typeof navigator.modelContext?.registerTool === 'function') {
        navigator.modelContext.registerTool(spec);
      }
    } catch (e) {}
  }
};
