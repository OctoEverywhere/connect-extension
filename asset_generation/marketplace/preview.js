const params = new URLSearchParams(location.search);
const requested = params.get('slide');
const slide = ['cover', 'monitoring', 'pause'].includes(requested) ? requested : 'cover';
const exportMode = params.get('export') === '1';
const selected = document.querySelector(`[data-slide="${slide}"]`);
document.querySelectorAll('[data-slide]').forEach(art => { art.hidden = art !== selected; });
document.body.classList.toggle('export', exportMode);
document.getElementById('export-link').href = `?slide=${slide}&export=1`;
document.querySelectorAll('.preview-nav a').forEach(link => {
  if (link.getAttribute('href') === `?slide=${slide}`) link.setAttribute('aria-current', 'page');
});
function fitPreview() {
  if (exportMode) return;
  const frame = document.getElementById('frame');
  const scale = Math.min(frame.clientWidth / 1920, 1);
  selected.style.transform = `scale(${scale})`;
  frame.style.height = `${1200 * scale}px`;
}
fitPreview();
window.addEventListener('resize', fitPreview);
