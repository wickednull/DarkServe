/* DarkServe service worker.
 *
 * Deliberately conservative: it only caches /static/ assets that carry a
 * version query (?v=N), never HTML or API responses. That keeps the app
 * installable and quick to open without ever serving a stale interface —
 * the server marks HTML no-store for the same reason.
 */
const CACHE = 'darkserve-static-v1';

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE).then((cache) =>
      cache.addAll([
        '/static/app.js',
        '/static/style.css',
        '/static/icon-192.png',
        '/static/icon-512.png',
      ])
    ).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))
    ).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // HTML and API always go to the network first.
  const isHTML = req.mode === 'navigate' || (req.headers.get('accept') || '').includes('text/html');
  const isStatic = url.pathname.startsWith('/static/');
  if (isHTML || !isStatic) return;

  // Versioned assets are immutable, so cache-first is safe.
  event.respondWith(
    caches.match(req).then((hit) =>
      hit || fetch(req).then((res) => {
        if (res.ok && url.search.includes('v=')) {
          const copy = res.clone();
          caches.open(CACHE).then((cache) => cache.put(req, copy));
        }
        return res;
      }).catch(() => hit)
    )
  );
});
