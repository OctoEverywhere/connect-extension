# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Prusa Connect Local operators who want OctoEverywhere's Gadget AI to watch their prints from their own Connect server. Administrators configure credentials and printer controls; other authorized users can view monitoring.

## Product Purpose

Deliver a real OctoEverywhere extension that checks active prints for likely failure, presents results and history, and can request automatic pauses when an operator enables that behavior.

## Operating Context

The extension is a native Go process supervised by Prusa Connect Local, with a Svelte module inside Connect's existing application. The first version calls the public Gadget HTTP API directly. The user explicitly selected automatic monitoring startup, accepting an estimate based on reported printing state, progress and a configurable warm-up delay.

## Capabilities and Constraints

- Connect a Gadget account by validating and saving its API key.
- Configure a read-only Connect token for camera snapshots. The server address comes from the extension's declared configuration.
- Select monitored printers and their cameras. Configure warning/pause confidence and a warm-up buffer per printer.
- Automatic pausing is off by default. A recommendation or submitted request must never be described as a completed physical pause without confirmation.
- Show monitoring, preparation, paused, offline, missing-camera, error and usage-limit states, plus activity history.
- Connect does not identify when the first layer begins. Firmware extrusion telemetry can improve the startup estimate but may include purge. Describe automatic startup as an estimate; do not require manual confirmation.
- Remote access and OE notifications are future features, not capabilities of this version.
- This version does not fork or depend on the main OE Python plugin. Future reuse must preserve that code as a separate dependency rather than an independent fork.
- Keep credentials server-side after setup. The module browser uses the host's session client and never creates an independent transport.
- Image privacy is a core commitment: Gadget API snapshots are used only for the individual inference request and deleted immediately after processing. They are never retained afterward, used for training or fine-tuning (including anonymized copies), or sold. Keep this promise prominent in the README, marketplace text, and setup notice, with a link to https://octoeverywhere.com/privacy. Distinguish images from retained credentials, settings, results, and account/usage records.

## Brand Commitments

Display name: OctoEverywhere. The user selected the existing OE account-page character: a flat gray background, floating box cards and a header bar. The actual website provides the colors: charcoal canvas and darker panels, white text, blue accents, and the OE purple CTA. Preserve the official OE/Gadget artwork and Inter typography. Do not substitute an unrelated generic dashboard identity.

## Evidence on Hand

- Existing OE source: `OctoStream/Pages/GadgetApiKey.cshtml`, `OctoStream/Pages/Login.cshtml`, and `OctoStream/wwwroot/css/site.css` in the sibling OctoStream repository.
- Local copies of original OE and Gadget marks and Inter font: `ui/src/assets/`, with provenance in its README.
- Real module code and endpoint contracts: `ui/src/api.ts`, `ui/src/page.svelte`, `openapi.json`.
- No synthetic printer data is part of the shipping interface.

## Product Principles

- Reuse the existing OE brand and Prusa's supported extension APIs.
- Make current monitoring behavior and operator control clear.
- Keep account setup separate from individual printer choices.
- Report actual state, limits and uncertainty without inventing evidence.
