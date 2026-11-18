---
name: OctoEverywhere Connect extension
description: OE account-style controls for Gadget print monitoring
colors:
  primary: "#78a4fa"
  primary-hover: "#a5bfff"
  action: "#5865f2"
  action-hover: "#4f5bda"
  canvas: "#36393f"
  card: "#292b2f"
  text: "#ffffff"
  muted: "#c1c4cc"
  border: "#454950"
  good: "#79d4a2"
  warning: "#f1c47a"
typography:
  headline:
    fontFamily: "OE Inter, Inter, Arial, sans-serif"
    fontSize: "29px"
    fontWeight: 750
    lineHeight: 1.2
    letterSpacing: "-0.025em"
  title:
    fontFamily: "OE Inter, Inter, Arial, sans-serif"
    fontSize: "20px"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "-0.015em"
  body:
    fontFamily: "OE Inter, Inter, Arial, sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.55
  label:
    fontFamily: "OE Inter, Inter, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 600
rounded:
  control: "5px"
  card: "5px"
  shell: "8px"
spacing:
  control: "8px"
  group: "18px"
  section: "30px"
components:
  button-primary:
    backgroundColor: "{colors.action}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "9px 15px"
  button-primary-hover:
    backgroundColor: "{colors.action-hover}"
    textColor: "{colors.text}"
  card:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    rounded: "{rounded.card}"
    padding: "25px"
  input:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "10px 12px"
---

# Design System: OctoEverywhere Connect extension

## Overview

**Creative North Star: "OctoEverywhere account pages"**

Follow the existing OE account pages: a flat charcoal canvas, darker floating boxes and matching header, white text, blue accents and the familiar purple CTA. The original OE and Gadget marks and locally bundled Inter carry the product's identity. Connect continues to own the surrounding application.

**Key Characteristics:**

- Charcoal surfaces with restrained tonal separation.
- Original OE artwork, blue links and purple actions.
- Clear forms, descriptive states, and compact printer facts.

## Colors

The OE blue accent belongs to links and focus. Purple actions retain the existing website CTA treatment. Charcoal surfaces carry the layout. Light green and amber communicate healthy and attention states alongside readable text, never by color alone.

**The Scoped Surface Rule.** Apply extension colors only inside the module; preserve Connect's surrounding interface and navigation.

## Typography

Inter is locally bundled from OE's existing assets. Headings are compact and moderately bold; supporting text is smaller and muted. The main heading becomes 24px on narrow screens. Numeric printer facts and timestamps use tabular numerals. Body copy stays within a readable 70-character measure.

## Layout

The content column is capped at 1140px with 30px desktop horizontal padding. The first-use flow places the account form beside a quieter explanatory column. Printer monitoring is a vertical list, with details and editable settings within each printer's card. Recent activity follows the printer list.

At 880px the setup columns become one. At 560px content insets reduce to 14px, printer facts become two columns and camera controls wrap. The page remains part of the Connect module, not a standalone navigation shell.

## Elevation & Depth

Dark panels sit on a lighter charcoal canvas. A restrained downward shadow (`0 3px 14px #15171b14`) supports this tonal separation without an outer border. The header has a shallower shadow (`0 2px 6px #15171b14`). Thin dividers separate settings and activity rows inside these surfaces.

## Shapes

Cards, inputs and buttons are simple boxes with the website's modest 5px corners. Small circles are reserved for setup-step numbers and status marks; avoid turning major surfaces into pills.

## Components

### Buttons

Compose Prusa's Button primitive. Primary actions use the existing purple-to-indigo website gradient with a 12% dark overlay for readable white labels; secondary actions use muted blue with white text. Pending and disabled states come from the real action lifecycle. Keyboard focus uses a 2px accent outline with 3px offset. Hover transitions take 150ms and are reduced when reduced motion is requested.

### Inputs

Compose Input, Checkbox, Label and Select primitives. Inputs are at least 43px high with a thin neutral stroke. Checkbox titles and supporting text share one stacked column beside the control, so neither competes for horizontal space. Secret values are only entered during setup and cleared after a successful save. Read-only server addresses are visually quieter and accompanied by configuration instructions.

### Cards

Cards group a meaningful unit: account setup, an individual printer, or activity history. Setup steps use internal separators. Printer cards offer a concise summary and explicitly expanded settings. Their structure is driven by real data, with no illustrative metrics.

### Navigation and status

The charcoal brand bar shows the original OE mark, its name, and a help link. Connect provides the application navigation. Status uses a short label and optional small dot; warning copy states what was detected and what the operator should check.

## Do's and Don'ts

### Do:

- **Do** preserve the original OE and Gadget artwork and Inter typography.
- **Do** pair state colors with readable labels and useful recovery instructions.
- **Do** keep editable printer settings with their printer and account credentials in connection settings.

### Don't:

- **Don't** replace the existing OE charcoal surfaces and original brand assets with an unrelated visual identity.
- **Don't** claim that Gadget is watching, a key is verified, or a printer has paused unless the backend reports that state.
- **Don't** restyle Connect outside the extension's own surface.
