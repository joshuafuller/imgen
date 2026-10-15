# Social cards from Markdown posts

Turn a folder of Markdown posts into 1200x630 link-preview cards. imgen draws one art plate per post. The script then draws the title and the site name as real text. Image models garble letters, so the plate has none, and the words stay exact and crisp. A title edit costs $0, because the plate comes from the cache.

| The plate (no text) | The card (text drawn locally) |
|---|---|
| ![Plate for the flaky tests post](flaky-tests-plate.jpg) | ![Card for the flaky tests post](cards/flaky-tests.jpg) |

## Steps

You need `imgen` with a Gemini key (`imgen init`) and `uv`. Run the commands from this folder (`examples/social-cards`).

1. Write the posts. Each one starts with front matter. Only `title` is required:

   ```text
   ---
   title: A Field Guide to Flaky Tests
   description: Five patterns that make a test pass on Monday and fail on Friday, and how to pin each one down.
   tags: [testing, ci]
   art: a glowing coin spinning in mid air, one face teal and the other amber, with soft motion trails
   ---
   ```

   The plate shows the `art` line, or the description if there is no `art` line. The tags are added as keywords. Three sample posts are in `posts/`.

2. Check the cost first. This calls no API and writes no file:

   ```text
   uv run --with pillow python -I make_cards.py posts cards --site "The Build Log" --dry-run
   ```

3. Build the cards (about 10 seconds; each new plate costs about $0.067):

   ```text
   uv run --with pillow python -I make_cards.py posts cards --site "The Build Log"
   ```

   A first run prints one line per card. For the sample posts it prints this:

   ```text
   flaky-tests: plate new ~$0.067 -> cards/flaky-tests.jpg  1200x630  138 KB  contrast 15.0:1
   lighthouse-on-call: plate new ~$0.067 -> cards/lighthouse-on-call.jpg  1200x630  123 KB  contrast 15.3:1
   offline-first: plate new ~$0.067 -> cards/offline-first.jpg  1200x630  156 KB  contrast 7.8:1
   3 cards in cards | plates: 3 new, 0 cached | API cost ~$0.201
   ```

4. Edit a title in `posts/` and run step 3 again. Every plate comes from the cache:

   ```text
   flaky-tests: plate cached $0 -> cards/flaky-tests.jpg  1200x630  139 KB  contrast 14.5:1
   lighthouse-on-call: plate cached $0 -> cards/lighthouse-on-call.jpg  1200x630  123 KB  contrast 15.3:1
   offline-first: plate cached $0 -> cards/offline-first.jpg  1200x630  156 KB  contrast 7.8:1
   3 cards in cards | plates: 0 new, 3 cached | API cost ~$0.000
   ```

   `imgen history` shows the three jobs at `$0.000`. The new title is on its card, and the other two cards are byte for byte the same.

## Outputs

| File | What |
|---|---|
| `cards/SLUG.jpg` | The card: 1200x630 JPEG, under 1 MB (these samples are 120 to 160 KB) |
| `PLATES/SLUG.jpg` | With `--plates PLATES` only: the plate as imgen made it, 1376x768, about 600 KB |
| `flaky-tests-plate.jpg` | A 600 px preview of one plate, for this page. The script does not make it |

SLUG is the post's file name without `.md`. A leading date such as `2026-10-15-` is dropped. Each card keeps the small comment that imgen writes into its images (imgen version, provider and model). It holds no prompt and no path.

## What makes it work

- **The title is not in the plate prompt.** The prompt has the scene, the tags and one shared style block. imgen caches by the whole request, so a title edit hits the cache.
- **The style block leaves room.** It puts the subject at the top right and keeps the lower left calm, where the title goes.
- **The type is real.** The script uses Inter Display ExtraBold. It picks the largest size at which the title fits in three lines, then breaks the lines to be about the same length. Quotes, dashes and dots become real typographic marks.
- **A scrim keeps it readable.** The script measures the title against the lightest part of the picture behind it. It darkens the gradient scrim until the contrast is at least 7:1, and prints the result.
- **The size is checked.** The JPEG quality drops until the file is under 1 MB. `--format png` makes PNG cards (about 650 to 750 KB here).

## Notes and limits

- The front matter reader is small. It takes `key: value`, `key: [a, b]` and `- item` lists. It is not a full YAML parser.
- A plate costs money only when its request changes: a new post, or an edit to the description, `art`, tags, `--style` or `--provider`, or a new default model in imgen. A new plate is about $0.067 on Gemini.
- The cache lives in `~/.cache/imgen/`. On a new machine, the plates are made again. `--plates DIR` saves them, but the script does not read them back.
- The shared style is the `STYLE` text in `make_cards.py`. Change it, or pass `--style "..."`, and every plate is made again.
- The plate is an AI image. Look at each card before you publish. If a plate shows a stray mark, change its `art` line and run again.
- Fonts: Inter if it is installed (free, SIL Open Font License), else DejaVu Sans Bold, else the font that comes with Pillow. `--font FILE` picks another.
- Tested with Gemini. Other providers go through `--provider`; the plate request is always `-r 1K -a 16:9`.
- Link previews are cached by the sites that show them. A new card file does not refresh an old preview.
