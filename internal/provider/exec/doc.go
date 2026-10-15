// Package exec is the plugin backend: it drives any image generator through
// an external command written in any language.
//
// Configure it with type "exec" and a command argv (a leading ~ in each arg
// is expanded). imgen runs the command with one subcommand appended.
//
// # generate
//
// Runs `<command...> generate` and writes one JSON object to stdin:
//
//	{"model","prompt","size","aspect_ratio","quality","format","background",
//	 "n","extra","options","inputs":[{"mime_type","path"}],"output_dir",
//	 "mask"?:{"mime_type","path"},"seed"?:int}
//
// "options" is the config's free-form options. Each input image is written to
// a temp dir first and passed by path. "output_dir" is a fresh temp dir the
// plugin may write result files into. Both are removed after imgen reads the
// results.
//
// "mask" is present only for a masked edit: the mask image (white or
// transparent marks the area to change) is written to the same temp dir.
// "seed" is present only when the user asked for repeatable output.
//
// The plugin prints one JSON object to stdout:
//
//	{"model"?: str,
//	 "images": [{"path": str} | {"mime_type": str, "b64": str}],
//	 "text"?: str, "error"?: str}
//
// Path images are read from disk; the MIME type comes from the extension.
// A non-zero exit, or a non-empty "error", fails the call. The error text is
// the "error" field, else the last 500 bytes of stderr.
//
// # models
//
// Runs `<command...> models` and reads from stdout:
//
//	{"models": [{"id": str, "created"?: int}]}
//
// # Environment
//
// The plugin does not get the parent environment. It gets PATH, HOME, USER,
// LOGNAME, LANG, TZ, TERM, TMPDIR, TEMP, TMP, XDG_RUNTIME_DIR,
// XDG_CONFIG_HOME, XDG_CACHE_HOME, XDG_DATA_HOME, DISPLAY, WAYLAND_DISPLAY,
// DBUS_SESSION_BUS_ADDRESS, WSL_DISTRO_NAME, WSLENV, SYSTEMROOT, WINDIR and
// every LC_* variable when set (see internal/subenv), plus the config's env
// map, plus each parent variable named in the option "pass_env" (a list of
// names, for example ["COMFYUI_URL","HF_TOKEN"]). Plugins never receive API keys such as
// OPENAI_API_KEY or GEMINI_API_KEY unless listed in pass_env.
package exec
