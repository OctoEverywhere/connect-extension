# Marketplace artwork

Mode: Persuade. Scope: `asset_generation/marketplace/`. The current listing cover is designed for 256 × 165 and exported at 1536 × 990. The earlier 1920 × 1200 posters are retained as alternatives. Extend the established OctoEverywhere identity. This is artwork, not a new application flow.

## Direction contract

THESIS: Introduce Gadget through its recognizable original character and clear, restrained feature copy. No invented printer results, generic AI art, or future features.

OWN-WORLD: Inherit OE Inter, flat charcoal, darker floating panels, the authentic blue Gadget character and purple rocket. Preserve the marks exactly.

STORY: At the actual marketplace thumbnail size, Connect operators immediately recognize the friendly Gadget face and understand AI failure detection. Product details belong in the listing text and the separate host UI screenshot.

FIRST VIEWPORT: The current cover has exactly two elements on flat charcoal: a large two-line “AI Failure Detection” headline at left and the original Gadget head at right. Both elements are vertically centered in the canvas, retaining their horizontal positions and sizes. The OctoEverywhere logo and signature are removed. No card, footer bar, explanatory copy, platform label, or secondary benefit. At 256 × 165 the headline is 26px, not reduced poster body copy. The master uses the exact same geometry at 6× size. A separate screenshot shows the real Connect host in dark mode; demo states are identified as such.

FORM: User-specified logo-and-text promotional artwork, rendered from HTML. Code-led, fixed export canvas; no concept seed is needed for this narrow, pinned request. Static artwork has no motion or interactive product controls. `listing-cover.html` provides master and thumbnail sizes; the older `index.html` preview links select the three poster alternatives.

## Implemented surface treatment

### Current listing cover

`asset_generation/marketplace/listing-cover.css` uses the existing dark panel color (`#292b2f`) as one uninterrupted background. At the marketplace's 256 × 165 size, the white two-line Inter headline is 26px at weight 780 with -0.035em tracking and 1.05 line height; the original Gadget head is 103px wide. Both elements are independently centered vertically in the canvas, with their horizontal positions, sizes, copy and colors preserved. These two elements carry the entire composition after removal of the OctoEverywhere logo and signature. The 1536 × 990 master preserves the thumbnail geometry at six times its size.

### Host UI screenshot

`exports/connect-dark-ui.jpg` is a separate 1265 × 712 capture of the actual running Prusa Connect Local host in its native dark theme. It records the genuine local demo state with Gadget connected and monitoring off; it is not an inference result or a rendered mock interface. The README supplies the demo caption and notes that the installed UI may predate uncommitted source changes.

### Earlier poster alternatives

The superseded 1920 × 1200 posters remain available as alternatives. Their `asset_generation/marketplace/artwork.css` deliberately scales the existing Inter family: headings are 96px on the cover, 87px for monitoring and 89px for pausing, at weight 760 with -0.04em tracking. Supporting copy is 33–36px; the 55px Gadget introduction is a cover-specific emphasis. These sizes belong to the earlier fixed poster canvas and do not determine the current listing cover or application type ramp.

The posters retain the established charcoal canvas, darker panels, white text, muted gray and OE blue. Pale purple (`#a193ff`) fills the abstract pause symbol; light gray (`#d7dae0`) labels the snapshot diagram, and pale purple (`#c9c1ff`) identifies preview links. These are scoped marketing accents, not new global action or state colors. Original SVG colors remain unchanged. Eight-pixel panel corners and flat tonal separation support the larger artwork; the posters add no shadows or simulated product controls.

The existing `DESIGN.md` and `.impeccable/design.json` remain the application-wide authority. Asset origins and the export workflow are recorded in `asset_generation/marketplace/README.md`; all seven JPEGs carry source comments written by `stamp-provenance.mjs`: the current master, actual-size proof, host UI screenshot, three earlier posters and their contact sheet. This surface note records the implementation without changing the global visual system.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
