# Animate a game character

Turn one character image into a seamless, looping sprite animation: a walk, an idle, an attack. Image models cannot draw a smooth animation frame by frame: the poses come out of order and the character drifts. A video model draws motion, so this example animates the character with Veo and then cleans the video into a game-ready loop.

![Approved knight walk, pixel style](knight-walk.gif)

## Steps

1. Make the character with imgen, as a cutout:

   ```text
   imgen gen -p openai -q high -r 1024x1024 -b transparent -f png --name hero \
     "pixel-art knight, side view facing right, full body, 16-bit game sprite style, clean outline, no background, no text"
   ```

2. Animate it on a green screen with Veo (about $0.40 for 4 seconds):

   ```text
   set -a; . ~/.config/imgen/env; set +a
   uv run --with pillow python -I make_video.py hero.png walk.mp4 --character "pixel-art knight"
   ```

3. Turn the video into a sprite loop (free, and you can run it again on the same video at any time):

   ```text
   uv run --with pillow --with numpy --with scipy python -I video2sprite.py walk.mp4 hero.png out/ --style pixel --name knight
   uv run --with pillow --with numpy --with scipy python -I video2sprite.py walk.mp4 hero.png out-smooth/ --style smooth --name knight
   ```

`--style pixel` gives crisp pixel art on a true pixel grid in the hero's own palette. `--style smooth` gives full-color, anti-aliased frames for painted or illustrated characters. Other actions use the same steps with `--action`, for example `--action "breathes in a calm idle loop"` or `--action "raises the sword and strikes forward"`.

## Outputs

| File | What |
|---|---|
| `frame_NNN.png` | Each frame, transparent |
| `NAME.gif` | The loop, transparent |
| `NAME-dark.gif` | The loop on a dark background, for previews |
| `NAME-sheet.png` | All frames in one row |
| `NAME-atlas.json` | TexturePacker-style frame data (hash format) that game engines read |

The script prints the loop it found, for example `26 frames (1.08 s loop from frame 48), seam 0.054 < motion 0.101`. A seam smaller than the motion between two frames means the loop has no visible jump.

## What makes it work

- **No black keying.** Only the green screen is removed, so dark pixel-art outlines survive.
- **No position pinning.** Veo keeps the camera still; pinning the feet to a line would add bounce.
- **One shared crop** fits the widest frame, so a swung sword is never cut off.
- **Loop search** compares every frame (legs weighted most) and picks the start and length that flow back into themselves.

Veo draws a new animation on every run. If a result turns, stiffens or swings oddly, run step 2 again; keep every video, since step 3 can rebuild any of them for free.
