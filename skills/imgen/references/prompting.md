# Prompting

## Structure

Write one line per idea, in this order:

1. **Subject**: what it is, with concrete nouns and counts ("three soldiers and one light vehicle").
2. **Setting**: place, time of day, weather.
3. **Style anchor**: medium and finish ("flat vector illustration", "studio product photo", "isometric 2.5D", "cinematic concept art").
4. **Light and color**: "dusk, deep navy and teal, warm rim light".
5. **Framing**: "wide shot, three-quarter elevated view, subject centered, space on the left for text".
6. **Hard rules**: "no text, no letters, no logos, no watermark, no flags".

For a polished commercial look, name the quality bar: "premium, crisp, the kind of art a top design studio makes for a keynote". Then add what to avoid: "not flat clip-art, not cartoon, not photo-real".

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

## Edits

- Name what changes **and** what must stay: "make the cube blue; keep the table, light and camera identical".
- One change per edit works best. Chain several edits rather than one big edit.
- If an edit changes too much, generate again from the original, not from the edited copy.
