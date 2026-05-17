package headlessserver

import (
	"fmt"
	"strings"
)

// shimHeader is the prelude of the injected runtime: it sets up window.runtime
// and window.go.main.App so the main bundle can import wailsjs files without
// rewriting them. The companion shimBindingsTemplate fills in the actual
// method bindings reflected from the App at startup.
const shimHeader = `(function(){
  if (window.__erunHeadlessInstalled) { return; }
  window.__erunHeadlessInstalled = true;

  var listeners = Object.create(null);
  var eventSourceStarted = false;
  var clipboard = "";

  function ensureEventSource() {
    if (eventSourceStarted) return;
    eventSourceStarted = true;
    try {
      var es = new EventSource("/__erun_events");
      es.onmessage = function(ev) {
        try {
          var payload = JSON.parse(ev.data);
          var cbs = listeners[payload.name];
          if (!cbs) return;
          // Iterate a snapshot so callbacks that unsubscribe don't
          // skip entries.
          var snapshot = cbs.slice();
          for (var i = 0; i < snapshot.length; i++) {
            var entry = snapshot[i];
            if (entry.remaining > 0) {
              entry.remaining--;
              if (entry.remaining === 0) {
                var idx = cbs.indexOf(entry);
                if (idx >= 0) cbs.splice(idx, 1);
              }
            }
            try { entry.callback.apply(null, payload.args || []); } catch (e) { console.error(e); }
          }
        } catch (e) {
          console.error("erun headless event parse failure", e);
        }
      };
    } catch (e) {
      console.error("erun headless EventSource failed", e);
    }
  }

  function EventsOnMultiple(name, callback, maxCallbacks) {
    ensureEventSource();
    if (!listeners[name]) listeners[name] = [];
    var entry = { callback: callback, remaining: maxCallbacks };
    listeners[name].push(entry);
    return function() {
      var arr = listeners[name];
      if (!arr) return;
      var idx = arr.indexOf(entry);
      if (idx >= 0) arr.splice(idx, 1);
    };
  }

  function EventsOn(name, callback) {
    return EventsOnMultiple(name, callback, -1);
  }

  function EventsOnce(name, callback) {
    return EventsOnMultiple(name, callback, 1);
  }

  function EventsOff(name /* , ...rest */) {
    var names = [name].concat(Array.prototype.slice.call(arguments, 1));
    for (var i = 0; i < names.length; i++) {
      delete listeners[names[i]];
    }
  }

  function EventsOffAll() {
    listeners = Object.create(null);
  }

  function EventsEmit(name /* , ...args */) {
    var args = Array.prototype.slice.call(arguments, 1);
    try {
      fetch("/__erun_emit", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name, args: args })
      });
    } catch (e) {
      console.error("erun headless emit failed", e);
    }
  }

  function noop() {}
  function returnEmpty() { return Promise.resolve(""); }

  function ClipboardSetText(text) {
    clipboard = text || "";
    try {
      fetch("/__erun_clipboard", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action: "set", text: clipboard })
      });
    } catch (e) {}
    if (navigator && navigator.clipboard && navigator.clipboard.writeText) {
      try { navigator.clipboard.writeText(clipboard); } catch (e) {}
    }
    return Promise.resolve(true);
  }

  function ClipboardGetText() {
    return fetch("/__erun_clipboard", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "get" })
    }).then(function(r) { return r.json(); }).then(function(p) {
      clipboard = (p && p.text) || "";
      return clipboard;
    }).catch(function() { return clipboard; });
  }

  window.runtime = {
    EventsOn: EventsOn,
    EventsOnMultiple: EventsOnMultiple,
    EventsOnce: EventsOnce,
    EventsOff: EventsOff,
    EventsOffAll: EventsOffAll,
    EventsEmit: EventsEmit,
    LogPrint: function(m) { console.log(m); },
    LogTrace: function(m) { console.log(m); },
    LogDebug: function(m) { console.debug(m); },
    LogInfo: function(m) { console.info(m); },
    LogWarning: function(m) { console.warn(m); },
    LogError: function(m) { console.error(m); },
    LogFatal: function(m) { console.error(m); },
    WindowSetTitle: noop,
    WindowToggleMaximise: noop,
    WindowMaximise: noop,
    WindowMinimise: noop,
    WindowUnmaximise: noop,
    WindowReload: function() { window.location.reload(); },
    WindowReloadApp: function() { window.location.reload(); },
    WindowIsMaximised: function() { return false; },
    WindowIsMinimised: function() { return false; },
    WindowIsNormal: function() { return true; },
    WindowFullscreen: noop,
    WindowUnfullscreen: noop,
    WindowIsFullscreen: function() { return false; },
    BrowserOpenURL: function(url) { try { window.open(url, "_blank", "noopener"); } catch (e) {} },
    ClipboardGetText: ClipboardGetText,
    ClipboardSetText: ClipboardSetText,
    Environment: function() {
      return Promise.resolve({ buildType: "headless", platform: "headless", arch: "headless" });
    },
    Quit: noop,
    Hide: noop,
    Show: noop,
    Screens: function() { return Promise.resolve([]); }
  };

  function bind(name) {
    return function() {
      var args = Array.prototype.slice.call(arguments);
      return fetch("/__erun_invoke", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ method: name, args: args })
      }).then(function(r) {
        return r.json().then(function(envelope) {
          if (envelope && envelope.error) {
            return Promise.reject(new Error(envelope.error));
          }
          return envelope ? envelope.data : undefined;
        });
      });
    };
  }

  window.go = window.go || {};
  window.go.main = window.go.main || {};
  window.go.main.App = window.go.main.App || {};
`

const shimFooter = `
  ensureEventSource();
})();`

// buildShimJS produces the JS prelude that runs before the React bundle in
// headless mode. Each exported method on the target gets a thin window.go
// binding that forwards to /__erun_invoke; the rest of the prelude wires up
// window.runtime with EventSource-backed event dispatch.
func buildShimJS(methods []string) string {
	var b strings.Builder
	b.WriteString(shimHeader)
	for _, m := range methods {
		// Escape the method name in case of unusual characters. Method
		// names are Go identifiers so this is defensive only.
		safe := strings.ReplaceAll(m, `"`, `\"`)
		fmt.Fprintf(&b, `  window.go.main.App["%s"] = bind("%s");`+"\n", safe, safe)
	}
	b.WriteString(shimFooter)
	return b.String()
}

// injectShim returns html with a <script> tag containing shim inserted as
// early as possible inside <head>. Wails normally injects its bindings before
// the bundle runs; doing the same here means modules importing
// wailsjs/runtime/runtime resolve to our shim with no extra rewriting.
func injectShim(html, shim string) string {
	tag := "<script>" + shim + "</script>"
	lower := strings.ToLower(html)
	if idx := strings.Index(lower, "<head>"); idx >= 0 {
		insertAt := idx + len("<head>")
		return html[:insertAt] + tag + html[insertAt:]
	}
	if idx := strings.Index(lower, "<html"); idx >= 0 {
		// Append after <html ...> opening tag.
		end := strings.Index(html[idx:], ">")
		if end >= 0 {
			insertAt := idx + end + 1
			return html[:insertAt] + "<head>" + tag + "</head>" + html[insertAt:]
		}
	}
	return tag + html
}
