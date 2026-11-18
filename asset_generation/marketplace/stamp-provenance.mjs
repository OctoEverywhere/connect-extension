import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const names = ['01-gadget-ai-cover.jpg', '02-active-print-monitoring.jpg', '03-optional-automatic-pause.jpg', 'contact-sheet.jpg', 'listing-cover.jpg', 'listing-cover-256x165.jpg', 'connect-dark-ui.jpg'];
const prefix = 'OctoEverywhere marketplace artwork: ';
for (const name of names) {
  const path = fileURLToPath(new URL(`exports/${name}`, import.meta.url));
  let jpeg = await readFile(path);
  if (jpeg[0] !== 0xff || jpeg[1] !== 0xd8) throw new Error(`${name}: expected JPEG`);
  // Remove our own leading comment when rerun. Preserve the encoded image bytes.
  if (jpeg[2] === 0xff && jpeg[3] === 0xfe) {
    const length = jpeg.readUInt16BE(4);
    if (jpeg.subarray(6, 4 + length).toString('utf8').startsWith(prefix)) {
      jpeg = Buffer.concat([jpeg.subarray(0, 2), jpeg.subarray(4 + length)]);
    }
  }
  const comment = Buffer.from(prefix + JSON.stringify({
    file: name,
    origin: name === 'connect-dark-ui.jpg'
      ? 'Direct browser viewport screenshot of the running OctoEverywhere extension inside Prusa Connect Local at loopback port 18480. Native host dark theme; genuine demo state, monitoring disabled. No synthetic detection results.'
      : name.startsWith('listing-cover')
      ? `Native browser screenshot of asset_generation/marketplace/listing-cover.html and listing-cover.css; ${name.includes('256x165') ? '256x165' : '1536x990'}.`
      : name === 'contact-sheet.jpg'
      ? 'Native browser screenshot of asset_generation/marketplace/contact-sheet.html; three 640x400 previews in a 1920x470 sheet.'
      : 'Native full-page browser screenshot of asset_generation/marketplace/index.html and artwork.css; 1920x1200.',
    artwork: name.startsWith('listing-cover')
      ? 'Original OctoEverywhere Gadget SVG only; OctoEverywhere signature intentionally omitted. See README.md for source paths. No generated imagery or printer data.'
      : 'Original OctoEverywhere Gadget and rocket SVGs; see README.md for source paths. No generated imagery or live printer data.',
    font: 'Inter, SIL Open Font License 1.1; see repository LICENSE.',
  }), 'utf8');
  const marker = Buffer.alloc(4);
  marker[0] = 0xff;
  marker[1] = 0xfe;
  marker.writeUInt16BE(comment.length + 2, 2);
  await writeFile(path, Buffer.concat([jpeg.subarray(0, 2), marker, comment, jpeg.subarray(2)]));
  console.log(`Added source metadata: ${name}`);
}
