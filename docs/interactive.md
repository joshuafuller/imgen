# Interactive mode

Interactive mode is a terminal session for people. You type a prompt and get an image. Then you type what to change, and imgen edits that image. You do not type flags or file names again. For scripts and AI agents, use `imgen gen` and `imgen edit`. See [CLI reference](cli.md).

## Start a session

```sh
imgen                      # on a terminal, a bare imgen starts a session
imgen -i                   # the same, always
imgen interactive -p openai -o renders/
```

A bare `imgen` starts a session only when stdin and stderr are both terminals. In a pipe or a script, it prints the help, as before. `imgen -i` and `imgen interactive` without a terminal stop with exit code 2 and this message: `interactive mode needs a terminal (stdin and stderr must be a TTY). In scripts, pipes and MCP use imgen gen`. The MCP server does not offer interactive mode.

The start flags set the first values of the settings. You can change all of them in the session.

| Flag | Meaning |
|---|---|
| `-p`, `--provider NAME` | Provider instance. Default `gemini`, or the provider that the profile sets. |
| `-m`, `--model ID` | Model ID or `latest`. Default: the provider default. |
| `-a`, `--aspect R` | Aspect ratio, such as `16:9`. |
| `-r`, `--resolution R` | Resolution or size, such as `2K` or `1024x1024`. |
| `-n`, `--count N` | Variants per prompt, from 1 to 9. |
| `--style NAME` | A named style from the rules. |
| `--profile NAME` | A flag profile from the rules. |
| `-o`, `--output DIR` | Output folder. Default: the current folder. |

## A short session

```text
imgen › a red fox in snow
✓ red-fox-in-snow.png · gemini-3.1-flash-image · 8.8s · ~$0.067 · session ~$0.067
imgen › make it bluer
✓ make-it-bluer.png · gemini-3.1-flash-image · 9.1s · ~$0.067 · session ~$0.134
imgen › /n 2
n: 2
imgen › new: a lighthouse at dusk
  1  lighthouse-at-dusk-1.png
  2  lighthouse-at-dusk-2.png
pick  1 [2]  ←/→ or 1-2, Enter
✓ lighthouse-at-dusk-2.png · 2 of 2 · gemini-3.1-flash-image · 9.4s · ~$0.134 · session ~$0.268
imgen › /history
  1  a red fox in snow  red-fox-in-snow.png
  2  make it bluer  make-it-bluer.png
* 3  a lighthouse at dusk  lighthouse-at-dusk-2.png
imgen › /quit
```

While a call runs, a spinner shows the time. When the call is done, the image shows inline if the terminal can show images (kitty, iTerm2 or WezTerm today). Then one status line shows the saved file, the model, the time, the cost of the call and the total spend of the session.

## Prompts and follow-ups

- The first prompt generates a new image with the current settings.
- The next prompt edits the current image. `make it bluer` runs `imgen edit -i <current image> "make it bluer"`.
- To start a new image, begin the line with `new:` (`new: a lighthouse at dusk`), or type `/new` and then the prompt.
- With `/n 2` or more, you get a numbered contact sheet of the variants. Pick one with the arrow keys, a digit, or Enter (Enter keeps the selected one). The pick becomes the current image. The other variants stay on disk.

## Commands

| Command | What it does |
|---|---|
| `/new [PROMPT]` | Start a new image instead of an edit of the current one. |
| `/provider NAME` | Set the provider. This also resets the model to the default of that provider. |
| `/model [ID]` | Set the model. With no ID, use the provider default. |
| `/aspect [R]` | Set the aspect ratio. With no value, use the default. |
| `/res [R]` | Set the resolution. With no value, use the default. |
| `/n N` | Make N variants (1 to 9) for each prompt, and then pick one. |
| `/style [NAME]` | Use a named style. `/style` or `/style none` removes it. |
| `/profile [NAME]` | Use a flag profile. `/profile` or `/profile none` removes it. |
| `/undo` | Go back to the previous image. Files are not deleted. |
| `/history` | List the images of this session. `*` marks the current image. |
| `/open` | Open the current image in the default viewer. |
| `/copy` | Copy the current image to the clipboard. |
| `/save NAME` | Copy the current image to `NAME` in the output folder. If the name has no extension, imgen adds the extension of the image. This command does not overwrite a file. |
| `/settings` | Show the settings, the session spend and `IMGEN_BUDGET`. |
| `/help` | List the commands. |
| `/quit` | Leave. Ctrl-D on an empty line also leaves. |

The Tab key completes the command names and the names of providers, styles and profiles.

## Keys

| Key | At the prompt | During a call |
|---|---|---|
| Left, Right, Home, End | Move the cursor. Ctrl-A and Ctrl-E also go to the start and the end. | Kept for the next prompt. |
| Backspace, Delete | Delete a character. Ctrl-U clears the line. | Kept for the next prompt. |
| Up, Down | Go through the prompts of this session. | Kept for the next prompt. |
| Ctrl-C | Clear the line. Two Ctrl-C in a row leave. | Cancel the call. |
| Ctrl-D | Leave when the line is empty. | Kept for the next prompt. |

## Files, budget and history

Each call goes through the same code as `imgen gen` and `imgen edit`. Thus all of these apply:

- Files go to the current folder, or to the `-o` folder. The name comes from the prompt. imgen never overwrites a file: it adds `-1`, `-2`, and so on.
- `IMGEN_BUDGET` stops a call before it is sent if the call goes over the daily cap. The session shows the error and continues. The session never uses `--force`.
- Each call goes into the history with its arguments and the prompt, so `imgen rerun` can replay it. The redaction rules and `IMGEN_HISTORY` apply.
- imgen validates each returned image before it saves the image.

## Terminal safety

The session puts the terminal in raw mode to read keys one at a time. It turns off echo, line buffering, the signal keys and flow control. It keeps output processing on. imgen restores the terminal when you quit, at the end of the input, after Ctrl-C twice, after a panic, and after a SIGTERM or a SIGHUP. Ctrl-C does not send a signal in raw mode. Thus a Ctrl-C during a call cancels only that call (and stops an exec plugin), and the session continues.

Interactive mode works on Linux and macOS. On Windows, use `imgen gen` and `imgen edit`.
