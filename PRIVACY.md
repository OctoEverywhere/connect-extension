# Image privacy and data handling

**Your print images stay yours.** OctoEverywhere has a strict image privacy policy for the Gadget API used by this extension: images are deleted immediately after processing and are never retained afterward, used for AI training, or sold.

## Camera images

When monitoring is enabled and a print passes the startup gate, the extension reads a fresh snapshot from the selected camera through your Prusa Connect server and sends it to the Gadget API over HTTPS.

Each image is used only to analyze that individual request. Once processing completes and the request finishes, the image is deleted from Gadget's servers. Images are not archived, kept for later requests or the duration of a print, or retained as anonymized copies. They are never used to train, retrain, fine-tune, or improve models, added to datasets, sold, or reused for another purpose. This applies to both free and paid Gadget API use.

The extension handles snapshot bytes in memory and does not write image files to its data directory. **Test camera** checks a snapshot locally without sending it to Gadget. Your camera or Connect server may independently store its own snapshots; this extension does not change those systems' storage settings.

## Records separate from images

The extension saves credentials, printer/camera selections, monitoring settings, print-context state, detection results, and activity history in the private data directory granted by Connect. It also keeps a snapshot hash and timestamp to detect repeated frames; these are not image copies. Backups of that directory include the saved credentials.

The Gadget API retains account, usage, billing, technical-log, and detection-context records needed to operate and secure the service. These records do not include submitted images. The image-deletion promise does not mean that settings or account records are erased after each analysis.

## Full policy

See the **Gadget Developer API** section of [OctoEverywhere's privacy policy](https://octoeverywhere.com/privacy) for the governing image-handling rules and account-data details. Its API-specific image protections take precedence over general image-retention statements elsewhere in that policy.

This file explains the extension's data flow; it does not replace the published privacy policy.
