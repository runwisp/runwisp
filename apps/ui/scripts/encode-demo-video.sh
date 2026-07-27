#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Encodes the two committed docs assets from the lossless PNG frames captured by
# the demo-video tour (screencast.ts writes them + a frames.txt concat list):
#   - runwisp-demo.webp : animated WebP (full color, autoplays + loops inline in
#                         an <img> on GitHub) — the README hero.
#   - runwisp-demo.mp4  : H.264 (faststart) — docs site <video> + social.
#
# The frames are lossless and captured at 2× device pixels, so this is a single
# lossy generation (PNG -> WebP/MP4) with a sharp downscale — no VP8 mush.
#
# Run after `playwright test --config playwright.demo-video.config.ts` (or via
# `bun run demo-video`, which chains both). Prefers `ffmpeg` on PATH; falls back
# to the host's ffmpeg through `flatpak-spawn --host` (this dev box is Silverblue,
# where ffmpeg lives on the host, not in the toolbox).
#
# Quality knobs (env overrides):
#   DEMO_VIDEO_FPS        output frame rate               (default 24)
#   DEMO_VIDEO_WEBP_WIDTH WebP width in px                (default 1120)
#   DEMO_VIDEO_WEBP_Q     WebP quality 0-100, higher=big  (default 82)
#   DEMO_VIDEO_MP4_WIDTH  MP4 width in px                 (default 1280)
#   DEMO_VIDEO_MP4_CRF    H.264 CRF, lower=better/bigger  (default 20)

set -euo pipefail

ui_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$ui_dir/../.." && pwd)"
rec_dir="$ui_dir/test-results/demo-video"
frames_list="$rec_dir/frames/frames.txt"
out_dir="$repo_root/apps/docs/src/assets/screenshots"

fps="${DEMO_VIDEO_FPS:-24}"
webp_width="${DEMO_VIDEO_WEBP_WIDTH:-1120}"
webp_q="${DEMO_VIDEO_WEBP_Q:-82}"
mp4_width="${DEMO_VIDEO_MP4_WIDTH:-1280}"
mp4_crf="${DEMO_VIDEO_MP4_CRF:-20}"

# --- locate ffmpeg -----------------------------------------------------------
if command -v ffmpeg >/dev/null 2>&1; then
    ffmpeg() { command ffmpeg "$@"; }
elif command -v flatpak-spawn >/dev/null 2>&1; then
    echo "[encode] ffmpeg not on PATH — using host ffmpeg via flatpak-spawn"
    ffmpeg() { flatpak-spawn --host ffmpeg "$@"; }
else
    echo "[encode] ERROR: no ffmpeg found (not on PATH, no flatpak-spawn)." >&2
    exit 1
fi

# --- locate the captured frames ----------------------------------------------
if [[ ! -f "$frames_list" ]]; then
    echo "[encode] ERROR: no frames at $frames_list — record first." >&2
    exit 1
fi
echo "[encode] source: $frames_list ($(grep -c '^file ' "$frames_list") frames)"
mkdir -p "$out_dir"

webp="$out_dir/runwisp-demo.webp"
mp4="$out_dir/runwisp-demo.mp4"

# The concat demuxer replays each PNG for its recorded duration (variable fps);
# the fps filter resamples to a constant rate. -safe 0 allows absolute paths.

# --- animated WebP (README hero) ---------------------------------------------
echo "[encode] -> $webp (width ${webp_width}, q ${webp_q})"
ffmpeg -y -f concat -safe 0 -i "$frames_list" \
    -vf "fps=${fps},scale=${webp_width}:-2:flags=lanczos" \
    -c:v libwebp_anim -loop 0 -q:v "$webp_q" -compression_level 6 -an \
    "$webp"

# --- MP4 (docs site + social) ------------------------------------------------
echo "[encode] -> $mp4 (width ${mp4_width}, crf ${mp4_crf})"
ffmpeg -y -f concat -safe 0 -i "$frames_list" \
    -vf "fps=${fps},scale=${mp4_width}:-2:flags=lanczos" \
    -c:v libx264 -pix_fmt yuv420p -crf "$mp4_crf" -preset slow -movflags +faststart -an \
    "$mp4"

echo "[encode] done:"
ls -lh "$webp" "$mp4"
