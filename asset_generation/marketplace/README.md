# Marketplace artwork

## Current listing image and gallery screenshot

Use **`exports/listing-cover.jpg`** as the listing cover. It is 1536 × 990, exactly six times the marketplace's 256 × 165 display size supplied by the user. `exports/listing-cover-256x165.jpg` is the actual-size proof. The design has exactly two elements: the AI Failure Detection headline and original Gadget head. Both are vertically centered in the canvas, with their horizontal positions and sizes preserved. The OctoEverywhere logo and signature have been removed. No feature list, footer bar, or small explanatory copy.

The editable source is `listing-cover.html` and `listing-cover.css`. Open `listing-cover.html?size=thumbnail` to inspect the actual listing size, or `listing-cover.html` for the master. Capture the first 1536 × 990 pixels for the master or 256 × 165 for the proof after the local Inter font and SVGs load.

**`exports/connect-dark-ui.jpg`** is a direct screenshot of the running extension inside the actual Prusa Connect Local host with its native dark theme. It shows the local demo's genuine configured state: Gadget connected, monitoring off, and a demo MK4S printer. No monitoring settings, printer commands, or Gadget requests were triggered for the screenshot, and no detection results were invented. Suggested gallery caption: **“OctoEverywhere inside Prusa Connect Local — demo printer, before monitoring is enabled.”** This is not a real-print inference example. The installed demo UI may predate uncommitted source edits; this image does not demonstrate those changes.

The current cover and UI screenshot are bundled in `exports/octoeverywhere-listing-images.zip`. Use those for the listing; the older posters below are alternatives.

## Earlier poster alternatives

Three 1920 × 1200 (16:10) JPEG promotional images are retained in `exports/01-gadget-ai-cover.jpg`, `02-active-print-monitoring.jpg`, and `03-optional-automatic-pause.jpg`. They were superseded for the small listing card by the simpler cover above. These are promotional graphics, not screenshots of live printer results.

The size is an artwork choice matching the marketplace's landscape card proportions, not a statement of its upload requirements. The files contain only currently implemented AI failure detection features. There are no claims about remote access, external notifications, guaranteed detection, exact first-layer detection, pricing, or completed physical pauses.

## Preview and edit

Open `index.html` in a browser. It previews the fixed export canvas at the available width. Select an image with the navigation links. Edit copy in `index.html`, layout in `artwork.css`, and preview behavior in `preview.js`. All assets are local; previewing makes no Gadget requests and needs no account or printer.

For a local HTTP preview, run from this directory:

```sh
python -m http.server 18491 --bind 127.0.0.1
```

Open `http://127.0.0.1:18491/`. To export, open each full-size route and take a full-page screenshot after the font and images load:

- `/?slide=cover&export=1` → `exports/01-gadget-ai-cover.jpg`
- `/?slide=monitoring&export=1` → `exports/02-active-print-monitoring.jpg`
- `/?slide=pause&export=1` → `exports/03-optional-automatic-pause.jpg`

The export document is exactly 1920 × 1200 and hides the preview controls. Capture at browser zoom 100%, with no viewport larger than the document. Keep the actual screenshot encoding when naming the file. The supplied exports are native JPEG browser captures, not JPEGs renamed as PNGs.

After exporting, run `node stamp-provenance.mjs` from this directory to embed source attribution in the JPEG comment without changing or recompressing the pixels. `contact-sheet.html` shows the images at 640 × 400 for thumbnail review; its screenshot is a comparison sheet, not an additional marketplace slide.

## Asset provenance

- `assets/gadget-full.svg`: original OctoEverywhere artwork copied unchanged from `OctoStream/OctoStream/wwwroot/img/gadget/gadget.svg` in the sibling OctoStream repository.
- `assets/gadget-head.svg`: original OctoEverywhere artwork copied unchanged from this repository's `ui/src/assets/gadget.svg`.
- `assets/octoeverywhere.svg`: original OctoEverywhere mark copied unchanged from `ui/src/assets/octoeverywhere.svg`.
- `assets/inter.woff2`: the bundled OE Inter font copied unchanged from `ui/src/assets/inter.woff2`; SIL Open Font License notice in the repository's root `LICENSE` and `ui/src/assets/Inter-LICENSE.txt`.
- The camera/arrow geometry, pause symbol, copy and layout are authored in the HTML/CSS. No AI-generated illustrations or stock photography are used.
- Export origin: full-page browser captures of these local HTML/CSS compositions. No live or fabricated printer measurements are depicted.

This directory is marketing source and stays outside the runtime extension package. It does not change `module.yaml`, the release workflow, or the application UI.
