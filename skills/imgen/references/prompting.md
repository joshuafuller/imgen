# Prompting

## Structure

Write one line per idea, in this order:

1. **Subject**: what it is, with concrete nouns and counts ("three soldiers and one light vehicle").
2. **Setting**: place, time of day, weather.
3. **Style anchor**: medium and finish ("flat vector illustration", "studio product photo", "isometric 2.5D", "cinematic concept art").
4. **Light and color**: "dusk, deep navy and teal, warm rim light".
5. **Framing**: "wide shot, three-quarter elevated view, subject centered, space on the left for text".
6. **Hard rules**: "no text, no letters, no logos, no watermark, no flags".

For a polished commercial look, name the quality bar: "premium, crisp, the kind of art a top design studio makes for a keynote". Then add what to avoid: "not flat clip-art, not cartoon, not photo-real". Cliché effects (glows, lens flares, neon edges) read as amateur; leave them out unless asked.

## Negatives that matter

- `no text, no letters, no numbers`: always use this for plates, icons and wallpapers.
- `no logos, no brand marks, unbranded, generic`: use it for products, vehicles, aircraft, ships and uniforms.
- `no flags, no insignia, no patches`: use it for military or defense scenes.
- `isolated on a plain background`: use it for assets you will cut out or composite.

## Layout-heavy images (diagrams, maps, infographics)

- Describe positions in words: "castle on the left third; dragon in the sky top right; river across the bottom; empty sky on the left for icons".
- Name each item once, with a short visual description.
- Do **not** pass a rough sketch as `-i`, because the model copies its look. Pass a polished image instead, or none.
- Leave text out of the image, then add it in code (see [recipes.md](recipes.md#plate--overlay-diagrams-maps-labeled-slides)).

## Consistent series

- Reuse one style block, word for word, in every prompt.
- Pass the best earlier image as `-i` with "match this style exactly".
- Pin the model with `-m <exact id>`, not `latest`.
- Gemini: `--set temperature=0.3` lowers the variation.
- Check every image against the first: palette, outline weight and subject size drift between calls. Redraw outliers one at a time and keep the accepted set, because a fresh run gives a new look. `examples/illustration-pack/` measures this in code.

## Edits

- Name what changes **and** what must stay: "make the cube blue; keep the table, light and camera identical".
- One change per edit works best. Chain several edits rather than one big edit.
- If an edit changes too much, generate again from the original, not from the edited copy.

## Reference image as a layout guide

A precise image passed with `-i` controls layout far better than words. The model repaints the guide and keeps its structure.

- **QR art:** pass the exact QR code and say "dark squares become X; light squares become Y; keep the exact layout including the three corner squares" (`imgen qr` does this for you).
- **Anatomy and pose:** for a creature or a figure, first make a clean still in the exact pose and view ("seen from directly above; the small head with eyes and fangs at the TOP; the large abdomen at the BOTTOM, no eyes on it"), then edit or animate from that still. Spell out the anatomy, or models put a face on the largest body part.
- **Edits that must not move things:** say what stays ("keep the table, light and camera identical").

## Animation

Image models cannot draw a smooth animation frame by frame: poses come out of order, repeat or drift, even with a pose guide. A video model draws motion. To animate a character, make one still, animate it with a video model on a green screen, then key and loop the video in code (see the sprite recipe in [recipes.md](recipes.md#animated-game-character)). Never reverse a video to make an ending land: a reversed gait runs backward.
