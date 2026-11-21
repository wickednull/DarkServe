/* ============ DARKSERVE UI ============ */
// Declared first: the boot code below reads them.
const QUEUE_KEY = 'darkserve.queue';
let playQueue = [];   // upcoming items, played in order
let queueIndex = -1;
let currentUser = null;
let currentPage = 'home';
let currentMediaId = 0;
let currentTitle = '';
let compatActive = false;
let nextEpisode = null;
let autoNext = true;
// Casting. The Google Cast SDK is only loaded after the admin enables it.
let castCtx = null, castSession = null, castReady = false, castEnabled = false, castBase = '';
const CAST_APP_ID = 'CC1AD845'; // Default Media Receiver
let castMedia = null; // what is currently on the TV
let castPoster = '', castSubs = [];
let castToken = '', castLibId = 0, castStartAt = 0;
const API = '';

const PAGES = {
  home:      { title: 'Home',      sub: 'Your drives, libraries and unfinished films', fn: pageHome },
  libraries: { title: 'Libraries', sub: 'Every mounted drive and local media folder', fn: pageLibraries },
  drives:    { title: 'Drives',    sub: 'USB storage detected on this machine', fn: pageDrives },
  upload:    { title: 'Upload',    sub: 'Send video files straight onto a library', fn: pageUpload },
  downloads: { title: 'Downloads', sub: 'Save video or audio from a link onto a library', fn: pageDownloads },
  iptv:      { title: 'Live TV',   sub: 'Channels from M3U / M3U8 playlists', fn: pageIPTV },
  duplicates:{ title: 'Duplicates', sub: 'Titles that appear more than once', fn: pageDuplicates },
  search:    { title: 'Search',    sub: 'Find any indexed title', fn: pageSearch },
  settings:  { title: 'Settings',  sub: 'Drives, scanning, playback and IPTV', fn: pageSettings },
  users:     { title: 'Users',     sub: 'Accounts and access', fn: pageUsers }
};

window.onerror = function (msg) {
  const el = document.getElementById('login-error');
  if (el && document.getElementById('login-screen').classList.contains('active')) {
    el.textContent = 'UI ERROR: ' + msg;
  }
  return false;
};

/* ------------ boot ------------ */
(function boot() {
  fetch(API + '/api/user-count').then(r => r.json()).then(d => {
    if (d.count === 0) { setTab('setup'); document.getElementById('tab-setup').style.display = ''; }
    else { setTab('login'); }
  }).catch(() => setTab('login'));

  fetch(API + '/api/settings').then(function (r) { return r.ok ? r.json() : null; })
    .then(function (st) {
      if (!st) return;
      castEnabled = st.cast_enabled === '1';
      if (castEnabled) loadCastSdk();
      const btn = document.getElementById('player-cast');
      if (btn) btn.classList.toggle('hidden', !castEnabled);
    }).catch(function () {});
  fetch(API + '/api/network').then(function (r) { return r.ok ? r.json() : null; })
    .then(function (n) { if (n && n.base_url) castBase = n.base_url; }).catch(function () {});

  fetchMe().then(function (w) {
    if (w) {
      showMain(w);
      fetchJSON('/api/favorites').then(function (list) {
        list.forEach(function (m) { favSet[m.id] = true; });
        refreshFavButtons();
      }).catch(function () {});
    } else { showLogin(); }
  });
  try { playQueue = JSON.parse(localStorage.getItem(QUEUE_KEY) || '[]'); } catch (e) { playQueue = []; }
})();

function fetchMe() {
  return fetch(API + '/api/me').then(r => (r.ok ? r.json() : null)).catch(() => null);
}

/* ------------ screens ------------ */
function showLogin() {
  currentUser = null;
  pausePlayer();
  document.getElementById('login-screen').classList.add('active');
  document.getElementById('main-screen').classList.remove('active');
}

function showMain(user) {
  currentUser = user;
  document.getElementById('login-screen').classList.remove('active');
  document.getElementById('main-screen').classList.add('active');
  const isAdmin = user.role === 'admin';
  document.getElementById('nav-admin1').style.display = isAdmin ? '' : 'none';
  document.getElementById('nav-admin2').style.display = isAdmin ? '' : 'none';
  document.getElementById('current-user').textContent = user.username;
  document.getElementById('current-role').textContent = user.role.toUpperCase();
  document.getElementById('user-avatar').textContent = user.username.charAt(0).toUpperCase();
  loadPage('home');
}

/* ------------ tabs ------------ */
function setTab(name) {
  document.querySelectorAll('.tab-btn').forEach(b => b.classList.toggle('active', b.dataset.tab === name));
  document.getElementById('login-form').classList.toggle('hidden', name !== 'login');
  document.getElementById('register-form').classList.toggle('hidden', name !== 'setup');
  document.getElementById('login-error').textContent = '';
}

document.querySelectorAll('.tab-btn').forEach(b => {
  b.addEventListener('click', () => setTab(b.dataset.tab));
});

/* ------------ nav ------------ */
document.querySelectorAll('.nav-links li').forEach(li => {
  li.addEventListener('click', function () {
    document.querySelectorAll('.nav-links li').forEach(l => l.classList.remove('active'));
    this.classList.add('active');
    loadPage(this.dataset.page);
  });
});

document.getElementById('btn-scan').addEventListener('click', function () {
  const btn = this;
  btn.disabled = true;
  btn.textContent = 'SCANNING…';
  toast('SCAN STARTED');
  fetch(API + '/api/scan', { method: 'POST' }).catch(() => {});
  watchScan(function () {
    btn.disabled = false;
    btn.textContent = 'SCAN ALL';
    if (currentPage !== 'upload' && currentPage !== 'settings' && currentPage !== 'downloads') loadPage(currentPage);
  });
});

// Polls the scan status so the UI reflects real progress, not a fixed delay.
function watchScan(done) {
  let last = 0, ticks = 0;
  (function poll() {
    fetchJSON('/api/scan-status').then(function (st) {
      if (st.scanning) {
        ticks++;
        if (st.files !== last) {
          last = st.files;
          const btn = document.getElementById('btn-scan');
          if (btn) btn.textContent = 'SCANNING ' + last.toLocaleString();
        }
        setTimeout(poll, 1200);
        return;
      }
      if (last) toast('SCAN COMPLETE · ' + last.toLocaleString() + ' FILES CHECKED');
      if (done) done();
    }).catch(function () {
      if (done) done();
    });
  })();
}

document.getElementById('btn-logout').addEventListener('click', function () {
  fetch(API + '/api/logout', { method: 'POST' }).then(function () { showLogin(); });
});

document.getElementById('player-close').addEventListener('click', closePlayer);
document.getElementById('player-compat').addEventListener('click', startCompat);
document.getElementById('detail-modal').addEventListener('click', function (e) {
  if (e.target === this) closeDetail();
});
document.getElementById('help-modal').addEventListener('click', function (e) {
  if (e.target === this) closeHelp();
});
document.getElementById('help-close').addEventListener('click', closeHelp);
document.getElementById('player-full').addEventListener('click', playerFullscreen);
document.getElementById('player').addEventListener('dblclick', playerFullscreen);

// Close when clicking the dimmed backdrop (not the video itself).
document.getElementById('player-modal').addEventListener('click', function (e) {
  if (e.target === this) closePlayer();
});

document.addEventListener('keydown', function (e) {
  const modal = document.getElementById('player-modal');
  const detail = document.getElementById('detail-modal');
  if (!detail.classList.contains('hidden')) {
    if (e.key === 'Escape') closeDetail();
    return;
  }
  if (e.key === '?' && currentUser && !typing) {
    e.preventDefault();
    document.getElementById('help-modal').classList.remove('hidden');
    return;
  }
  const typing = e.target && (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'SELECT');
  if (modal.classList.contains('hidden')) {
    if (!typing && e.key === '/') {
      e.preventDefault();
      if (currentUser) {
        document.querySelectorAll('.nav-links li').forEach(l => l.classList.toggle('active', l.dataset.page === 'search'));
        loadPage('search');
        setTimeout(() => { const i = document.getElementById('search-input'); if (i) i.focus(); }, 0);
      }
    }
    return;
  }
  const v = document.getElementById('player');
  switch (e.key) {
    case 'Escape': closePlayer(); break;
    case ' ': e.preventDefault(); if (v.paused) v.play().catch(function () {}); else v.pause(); break;
    case 'ArrowRight': e.preventDefault(); v.currentTime = Math.min(v.duration || 1e9, v.currentTime + 10); break;
    case 'ArrowLeft': e.preventDefault(); v.currentTime = Math.max(0, v.currentTime - 10); break;
    case 'ArrowUp': e.preventDefault(); v.volume = Math.min(1, v.volume + 0.1); break;
    case 'ArrowDown': e.preventDefault(); v.volume = Math.max(0, v.volume - 0.1); break;
    case 'f': case 'F': playerFullscreen(); break;
    case 'm': case 'M': v.muted = !v.muted; break;
  }
});

// One delegated handler for every clickable card/button. Values travel in
// data-attributes, so titles with quotes or apostrophes can never break JS.
document.addEventListener('click', function (e) {
  const el = e.target.closest ? e.target.closest('[data-act]') : null;
  if (!el) return;
  const d = el.dataset;
  switch (d.act) {
    case 'library': openLibrary(Number(d.id), d.name); break;
    case 'show': openShow(Number(d.lib), d.folder, d.title, d.libName); break;
    case 'media': openMedia(Number(d.id), d.title); break;
    case 'details': openDetails(Number(d.id)); break;
    case 'compat': startCompat(); break;
    case 'close-detail': closeDetail(); break;
    case 'mark': markWatched(Number(d.id)); break;
    case 'dl-cancel': cancelDownload(Number(d.id)); break;
    case 'edit-meta': openMetaEditor(Number(d.id)); break;
    case 'find-subs': findSubtitles(Number(d.id)); break;
    case 'dl-sub': downloadSubtitle(Number(d.id), Number(d.file)); break;
    case 'close-meta': document.getElementById('meta-editor').innerHTML = ''; break;
    case 'fav': toggleFav(Number(d.id)); break;
    case 'queue-add': queueAdd(Number(d.id), d.title); break;
    case 'queue-play': queuePlayIndex(Number(d.idx)); break;
    case 'queue-clear': queueClear(); break;
    case 'apply-meta': selectMetaResult(el); break;
    case 'search-meta': searchMeta(Number(d.id)); break;
    case 'save-meta': saveMeta(Number(d.id)); break;
    case 'rescan-lib': rescanLibrary(Number(d.id)); break;
    case 'mount-drive': mountDrive(d.id); break;
    case 'reset-pass': showResetForm(Number(d.id), d.name || ''); break;
    case 'save-pass': saveNewPassword(Number(d.id)); break;
    case 'cancel-pass': loadPage('users'); break;
    case 'next-ep': playNext(); break;
    case 'speed': setSpeed(Number(d.rate)); break;
    case 'forget': forgetMedia(Number(d.id)); break;
    case 'back-libs': loadPage('libraries'); break;
    case 'go-settings': gotoPage('settings'); break;
    case 'iptv-list': loadIPTVList(Number(d.id), el); break;
    case 'iptv-refresh': refreshIPTV(Number(d.id)); break;
    case 'iptv-del': delIPTV(Number(d.id)); break;
    case 'channel': openStream(d.name, d.url); break;
    case 'del-user': delUser(Number(d.id)); break;
    case 'lib-filter': setLibFilter(d.filter); break;
  }
});

function playerFullscreen() {
  const v = document.getElementById('player');
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(function () {});
  } else if (v.requestFullscreen) {
    v.requestFullscreen().catch(function () {});
  }
}

document.getElementById('login-form').addEventListener('submit', async function (e) {
  e.preventDefault();
  const res = await postRaw('/api/login', {
    username: document.getElementById('login-username').value,
    password: document.getElementById('login-password').value
  });
  if (res.ok) {
    showMain(await res.json());
  } else {
    // The server knows more than "wrong password" — it also tells us when
    // the account is locked out for too many tries.
    document.getElementById('login-error').textContent =
      await errorText(res, 'ACCESS DENIED — invalid username or password');
  }
});

document.getElementById('register-form').addEventListener('submit', async function (e) {
  e.preventDefault();
  const p1 = document.getElementById('reg-password').value;
  const p2 = document.getElementById('reg-password2').value;
  if (p1 !== p2) {
    document.getElementById('login-error').textContent = 'Passwords do not match';
    return;
  }
  const res = await postRaw('/api/register', {
    username: document.getElementById('reg-username').value,
    password: p1
  });
  if (res.ok) {
    toast('ADMIN ACCOUNT CREATED');
    showMain(await res.json());
  } else {
    document.getElementById('login-error').textContent = await errorText(res, 'Setup failed');
  }
});

/* ------------ routing ------------ */
function gotoPage(page) {
  document.querySelectorAll('.nav-links li').forEach(l => l.classList.toggle('active', l.dataset.page === page));
  loadPage(page);
}

function loadPage(page) {
  const def = PAGES[page];
  if (!def) return;
  currentPage = page;
  document.getElementById('page-title').textContent = def.title.toUpperCase();
  document.getElementById('page-sub').textContent = def.sub;
  const c = document.getElementById('page-content');
  c.innerHTML = '<div class="loading">LOADING…</div>';
  Promise.resolve()
    .then(() => def.fn(c))
    .catch(err => showError(c, err));
}

function showError(c, err) {
  if (err && err.authExpired) return;
  c.innerHTML = empty('Something went wrong', (err && err.message) || 'Unknown error');
}

/* ------------ home ------------ */
async function pageHome(c) {
  const [stats, libs, cont, deck, recent, favs] = await Promise.all([
    fetchJSON('/api/stats'),
    fetchJSON('/api/libraries'),
    fetchJSON('/api/continue').catch(() => []),
    fetchJSON('/api/ondeck').catch(() => []),
    fetchJSON('/api/recent').catch(() => []),
    fetchJSON('/api/favorites').catch(() => [])
  ]);
  updateSysStats(stats);

  let html =
    '<section class="hero">' +
      '<div class="hero-copy">' +
        '<span class="eyebrow">ONLINE · ' + stats.drives + ' DRIVE' + (stats.drives === 1 ? '' : 'S') + '</span>' +
        '<h2>WELCOME, ' + esc(currentUser.username).toUpperCase() + '</h2>' +
        '<p>Media is played straight from your external drives. Nothing is copied to this machine.</p>' +
      '</div>' +
      '<div class="stat-row">' +
        statChip(stats.drives, 'DRIVES') +
        statChip(stats.libraries, 'LIBRARIES') +
        statChip(stats.media_items, 'ITEMS') +
        statChip(stats.users, 'USERS') +
        '<button class="btn btn-primary" id="btn-random" onclick="pickRandom()">SURPRISE ME</button>' +
      '</div>' +
    '</section>';

  if (cont.length) {
    html += sectionLabel('CONTINUE WATCHING') +
      '<div class="cw-row">' + cont.map(continueCard).join('') + '</div>';
  }

  // Next unwatched episode per series, so catching up is one click.
  if (deck.length) {
    html += sectionLabel('ON DECK') +
      '<div class="cw-row">' + deck.map(onDeckCard).join('') + '</div>';
  }

  if (favs.length) {
    html += sectionLabel('MY LIST') +
      '<div class="cw-row">' + favs.map(favCard).join('') + '</div>';
  }

  if (recent.length) {
    html += sectionLabel('RECENTLY ADDED') +
      '<div class="h-row">' + recent.map(mediaCard).join('') + '</div>';
  }

  html += sectionLabel('LIBRARIES');
  html += libs.length
    ? '<div class="grid">' + libs.map(libraryCard).join('') + '</div>'
    : empty('No libraries yet.', 'Connect a USB drive — indexing starts automatically');
  c.innerHTML = html;
}

function onDeckCard(m) {
  const ep = (m.season || m.episode)
    ? 'S' + String(m.season || 1).padStart(2, '0') + ' E' + String(m.episode || 0).padStart(2, '0')
    : '';
  return '<div class="cw-card deck-card" data-act="media" data-id="' + m.id + '" data-title="' + escAttr(m.title) + '">' +
    '<div class="media-thumb cw-thumb" style="' + thumbStyle(m.show || m.title) + '">' +
      posterImg(m) +
      '<span class="thumb-init">' + esc(initial(m.show || m.title)) + '</span>' +
      '<div class="play-glyph">&#9654;</div>' +
      (m.is_usb ? '<span class="media-usb-tag">USB</span>' : '') +
    '</div>' +
    '<div class="cw-bar"><div class="cw-fill" style="width:0%"></div></div>' +
    '<div class="cw-title">' + esc(m.show || m.title) + '</div>' +
    '<div class="cw-meta">' + esc(ep) + (m.remaining ? ' · ' + m.remaining + ' left' : '') + '</div>' +
  '</div>';
}

function favCard(m) {
  return '<div class="cw-card fav-card" data-act="media" data-id="' + m.id + '" data-title="' + escAttr(m.title) + '">' +
    '<div class="media-thumb cw-thumb" style="' + thumbStyle(m.title) + '">' +
      posterImg(m) +
      '<span class="thumb-init">' + esc(initial(m.title)) + '</span>' +
      '<div class="play-glyph">' + (m.audio ? '&#9834;' : '&#9654;') + '</div>' +
      '<span class="fav-star">&#9733;</span>' +
    '</div>' +
    '<div class="cw-title">' + esc(m.title) + '</div>' +
    '<div class="cw-meta">' + esc(m.library || '') + (m.year ? ' · ' + m.year : '') + '</div>' +
  '</div>';
}

// Picks a random unwatched movie across every library.
async function pickRandom() {
  toast('PICKING SOMETHING…');
  const libs = await fetchJSON('/api/libraries').catch(() => []);
  const items = [];
  for (const l of libs) {
    const media = await fetchJSON('/api/media/' + l.id).catch(() => []);
    if (!Array.isArray(media)) continue;
    for (const m of media) {
      if (m.type !== 'show' && !m.audio && !(m.progress_pct > 0)) items.push(m);
    }
  }
  if (!items.length) { toast('NOTHING UNWATCHED TO PICK', true); return; }
  const pick = items[Math.floor(Math.random() * items.length)];
  openMedia(pick.id, pick.title);
}

function sectionLabel(text) {
  return '<div class="section-label">' + text + '</div>';
}

function continueCard(m) {
  const pct = m.progress_pct || 0;
  return '<div class="cw-card' + (m.audio ? ' audio-card' : '') + '" data-act="media" data-id="' + m.id + '" data-title="' + escAttr(m.title) + '">' +
    '<div class="media-thumb cw-thumb" style="' + thumbStyle(m.title) + '">' +
      posterImg(m) +
      '<span class="thumb-init">' + esc(initial(m.title)) + '</span>' +
      '<div class="play-glyph">&#9654;</div>' +
      (m.is_usb ? '<span class="media-usb-tag">USB</span>' : '') +
    '</div>' +
    '<div class="cw-bar"><div class="cw-fill" style="width:' + pct + '%"></div></div>' +
    '<div class="cw-title">' + esc(m.title) + '</div>' +
    '<div class="cw-meta">' + esc(m.library || '') + ' · ' + fmtTime(m.position) + ' / ' + fmtTime(m.duration) + '</div>' +
  '</div>';
}

function fmtTime(s) {
  if (!s || s < 0) return '0:00';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = Math.floor(s % 60);
  return (h ? h + ':' + String(m).padStart(2, '0') : m) + ':' + String(sec).padStart(2, '0');
}

function libraryCard(lib) {
  return '<div class="card" data-act="library" data-id="' + lib.id + '" data-name="' + escAttr(lib.name) + '">' +
    (lib.is_usb ? '<span class="card-usb">USB</span>' : '') +
    '<div class="card-ic">' + (lib.is_usb ? '&#9733;' : '&#9635;') + '</div>' +
    '<div class="card-title">' + esc(lib.name) + '</div>' +
    '<div class="card-subtitle">' + esc(lib.path) + '</div>' +
  '</div>';
}

function statChip(n, label) {
  return '<div class="stat-chip"><b>' + (n || 0) + '</b><span>' + label + '</span></div>';
}

function updateSysStats(stats) {
  document.getElementById('stat-drives').textContent = stats.drives + ' DRIVES';
  document.getElementById('stat-media').textContent = stats.media_items + ' ITEMS';
}

/* ------------ libraries page ------------ */
async function pageLibraries(c) {
  const [libs, drives] = await Promise.all([
    fetchJSON('/api/libraries'),
    fetchJSON('/api/drives')
  ]);
  document.getElementById('stat-drives').textContent = drives.length + ' DRIVES';
  c.innerHTML = libs.length
    ? '<div class="grid">' + libs.map(libraryCard).join('') + '</div>'
    : empty('No libraries yet.', 'Plug in a USB drive — indexing is automatic');
}

/* ------------ drives page ------------ */
async function pageDrives(c) {
  const drives = await fetchJSON('/api/drives');
  const pending = (await fetchJSON('/api/drives/unmounted').catch(function () { return []; })) || [];
  let html = '<div class="grid">';
  for (const d of drives) {
    const pct = d.Size ? Math.round((d.Used / d.Size) * 100) : 0;
    html +=
      '<div class="card">' +
        '<div class="card-ic">&#9733;</div>' +
        '<div class="card-title">' + esc(d.Label || 'USB DRIVE') + '</div>' +
        '<div class="card-subtitle">' + esc(d.Mount) + ' · ' + esc((d.FSType || '').toUpperCase()) + '</div>' +
        '<div class="drive-cap"><div class="drive-fill" style="width:' + pct + '%"></div></div>' +
        '<div class="card-subtitle" style="margin-top:6px">' + fmtSize(d.Used) + ' / ' + fmtSize(d.Size) + ' · ' + pct + '%</div>' +
      '</div>';
  }
  html += '</div>';

  // Drives that are plugged in but not mounted. Without automount they stay
  // invisible, so the admin can mount them from here.
  if (pending.length) {
    html += '<div class="sect" style="margin-top:18px"><h4>Plugged in, not mounted</h4>';
    html += '<p class="hint-line">This system does not mount drives by itself. Mount one and DarkServe indexes it right away.</p>';
    html += '<table class="users-table"><thead><tr><th>Drive</th><th>Size</th><th></th></tr></thead><tbody>';
    for (const p of pending) {
      html += '<tr>' +
        '<td>' + esc(p.label || p.path) + '<div class="muted">' + esc(p.path) + '</div></td>' +
        '<td>' + fmtSize(p.size) + '</td>' +
        '<td class="right"><button class="btn btn-small" data-act="mount-drive" data-id="' + escAttr(p.path) + '">MOUNT</button></td>' +
        '</tr>';
    }
    html += '</tbody></table></div>';
  }

  c.innerHTML = (drives.length || pending.length)
    ? html
    : empty('No USB drives detected.', 'Connect one — DarkServe finds it automatically');
}

// Mounting a drive asks the desktop for permission, so it can take a moment
// and it may pop up a password prompt.
async function mountDrive(path) {
  const btn = document.querySelector('[data-act="mount-drive"][data-id="' + CSS.escape(path) + '"]');
  if (btn) { btn.disabled = true; btn.textContent = 'MOUNTING…'; }
  try {
    const res = await postRaw('/api/drives/mount', { device: path });
    if (!res.ok) throw new Error(await errorText(res, 'could not mount the drive'));
    toast('Mounted — indexing now');
  } catch (e) {
    toast(e.message || 'could not mount the drive', true);
    if (btn) { btn.disabled = false; btn.textContent = 'MOUNT'; }
    return;
  }
  setTimeout(function () { loadPage(currentPage); }, 1500);
}

/* ------------ player ------------ */
function pausePlayer() {
  const v = document.getElementById('player');
  v.classList.remove('audio-mode');
  v.removeAttribute('poster');
  if (window._hls) { try { window._hls.stopLoad(); window._hls.destroy(); } catch (e) {} window._hls = null; }
  v.pause();
  v.removeAttribute('src');
  v.load();
}

function closePlayer() {
  pausePlayer();
  stopCompatStream();
  currentMediaId = 0;
  document.getElementById('player-compat').classList.add('hidden');
  document.getElementById('player-modal').classList.add('hidden');
}

/* ------------ Upload page ------------ */
let pendingFiles = [];

async function pageUpload(c) {
  const libs = await fetchJSON('/api/libraries');
  if (!libs.length) {
    c.innerHTML = empty('No library to upload into.', 'Plug in the USB drive first');
    return;
  }
  // Prefer the first USB library as default target.
  const usbIdx = libs.findIndex(l => l.is_usb);
  const dflt = usbIdx >= 0 ? libs[usbIdx].id : libs[0].id;
  const opts = libs.map(l =>
    '<option value="' + l.id + '"' + (l.id === dflt ? ' selected' : '') + '>' +
    esc(l.name) + (l.is_usb ? ' [USB]' : '') + '</option>').join('');
  pendingFiles = [];
  c.innerHTML =
    '<div class="panel">' +
      '<div class="sect">' +
        '<h4>Destination</h4>' +
        '<div class="set-row"><div><label>Library</label><small>Files are streamed to this library, never cached on the server disk</small></div>' +
        '<select id="up-lib" class="sel">' + opts + '</select></div>' +
      '</div>' +
      '<div class="sect">' +
        '<h4>Files</h4>' +
        '<div id="dropzone" class="dropzone" tabindex="0">' +
          '<div class="dz-icon">&#8679;</div>' +
          '<p class="dz-main">Drop video files here</p>' +
          '<p class="dz-hint">or click to browse · video or audio · mkv mp4 avi webm ts · mp3 flac wav ogg m4a</p>' +
          '<input type="file" id="up-file" multiple accept="video/*,audio/*,.mkv,.mp4,.avi,.mov,.webm,.m4v,.mpg,.mpeg,.wmv,.ts,.mp3,.flac,.wav,.ogg,.m4a,.aac,.opus,.wma" style="display:none">' +
        '</div>' +
        '<ul id="up-list" class="up-list"></ul>' +
        '<div id="up-progress" class="up-progress hidden"><div class="up-bar"><div class="up-fill"></div></div><div class="up-txt">0%</div></div>' +
        '<div id="up-status"></div>' +
        '<div class="btn-row"><button class="btn btn-primary" id="btn-up-go">UPLOAD</button></div>' +
      '</div>' +
    '</div>';

  const dz = document.getElementById('dropzone');
  const fi = document.getElementById('up-file');
  dz.addEventListener('click', () => fi.click());
  dz.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); fi.click(); } });
  dz.addEventListener('dragover', e => { e.preventDefault(); dz.classList.add('drag'); });
  dz.addEventListener('dragleave', () => dz.classList.remove('drag'));
  dz.addEventListener('drop', e => {
    e.preventDefault();
    dz.classList.remove('drag');
    setPendingFiles(e.dataTransfer.files);
  });
  fi.addEventListener('change', () => setPendingFiles(fi.files));
  document.getElementById('btn-up-go').addEventListener('click', doUpload);
}

function setPendingFiles(list) {
  pendingFiles = Array.from(list || []);
  const ul = document.getElementById('up-list');
  ul.innerHTML = pendingFiles.map(f =>
    '<li><span class="up-name">' + esc(f.name) + '</span><span class="up-size">' + fmtSize(f.size) + '</span></li>'
  ).join('');
  document.querySelector('#dropzone .dz-main').textContent =
    pendingFiles.length ? pendingFiles.length + ' file(s) ready' : 'Drop video files here';
}

function doUpload() {
  const lib = document.getElementById('up-lib').value;
  if (!pendingFiles.length) { toast('ADD FILES FIRST', true); return; }
  const bar = document.getElementById('up-progress');
  const fill = bar.querySelector('.up-fill');
  const txt = bar.querySelector('.up-txt');
  const status = document.getElementById('up-status');
  bar.classList.remove('hidden');
  status.innerHTML = '';
  fill.style.width = '0%';
  txt.textContent = '0%';

  // The library field must precede the file parts for the server parser.
  const fd = new FormData();
  fd.append('library', lib);
  pendingFiles.forEach(f => fd.append('file', f, f.name));

  const xhr = new XMLHttpRequest();
  xhr.open('POST', API + '/api/upload');
  xhr.upload.addEventListener('progress', function (e) {
    if (e.lengthComputable) {
      const p = Math.round((e.loaded / e.total) * 100);
      fill.style.width = p + '%';
      txt.textContent = p + '%';
    }
  });
  xhr.onload = function () {
    if (xhr.status >= 200 && xhr.status < 300) {
      let d = {};
      try { d = JSON.parse(xhr.responseText); } catch (e) {}
      fill.style.width = '100%';
      txt.textContent = 'DONE';
      status.innerHTML = '<div class="up-done">' + (d.files || pendingFiles.length) + ' file(s) · ' + esc(d.dest || 'saved') + '</div>';
      toast('UPLOADED · ' + (d.files || pendingFiles.length) + ' FILE(S)');
      pendingFiles = [];
      document.getElementById('up-list').innerHTML = '';
      document.querySelector('#dropzone .dz-main').textContent = 'Drop video files here';
    } else {
      let msg = 'UPLOAD FAILED';
      try { msg = JSON.parse(xhr.responseText).error || msg; } catch (e) {}
      toast(msg, true);
      bar.classList.add('hidden');
    }
  };
  xhr.onerror = function () { toast('NETWORK ERROR', true); bar.classList.add('hidden'); };
  xhr.send(fd);
}

/* ------------ IPTV page ------------ */
async function pageIPTV(c) {
  const lists = (await fetchJSON('/api/iptv/lists')) || [];
  const isAdmin = currentUser && currentUser.role === 'admin';
  let html = '<div class="panel wide">';
  html += '<div class="sect"><h4>Playlists</h4>';
  if (lists.length) {
    html += '<div class="grid list-grid">';
    for (const l of lists) {
      html += '<div class="card" data-act="iptv-list" data-id="' + l.id + '">' +
        '<div class="card-ic">&#9650;</div>' +
        '<div class="card-title">' + esc(l.name) + '</div>' +
        '<div class="card-subtitle">' + l.channels + ' channels · ' + esc((l.type || 'url').toUpperCase()) + '</div>';
      if (isAdmin) {
        html += '<div class="card-actions">' +
          '<button class="btn btn-small" data-act="iptv-refresh" data-id="' + l.id + '">REFRESH</button>' +
          '<button class="btn btn-danger btn-small" data-act="iptv-del" data-id="' + l.id + '">DELETE</button></div>';
      }
      html += '</div>';
    }
    html += '</div>';
  } else {
    html += empty('No playlists yet.', isAdmin ? 'Add an M3U / M3U8 playlist in Settings' : 'Ask an admin to add a playlist');
  }
  if (isAdmin) {
    html += '<div class="toolbar-end"><button class="btn btn-ghost btn-small" data-act="go-settings">MANAGE PLAYLISTS IN SETTINGS →</button></div>';
  }
  html += '</div>';
  html += '<div id="iptv-browse"></div>';
  html += '</div>';
  c.innerHTML = html;
  if (lists.length) loadIPTVList(lists[0].id);
}

async function addIPTVList() {
  const name = document.getElementById('iptv-name').value.trim();
  const url = document.getElementById('iptv-url').value.trim();
  if (!name || !url) { toast('NAME + PLAYLIST URL REQUIRED', true); return; }
  toast('FETCHING PLAYLIST…');
  const res = await postRaw('/api/iptv/lists', { name: name, type: 'url', source: url });
  if (res.ok) { toast('PLAYLIST LOADED'); afterPlaylistChange(); }
  else toast(await errorText(res, 'LOAD FAILED'), true);
}

async function refreshIPTV(id) {
  toast('REFRESHING…');
  const res = await fetch(API + '/api/iptv/lists/' + id, { method: 'POST' });
  if (res.ok) { toast('REFRESHED'); afterPlaylistChange(); }
  else toast(await errorText(res, 'REFRESH FAILED'), true);
}

async function delIPTV(id) {
  const res = await fetch(API + '/api/iptv/lists/' + id, { method: 'DELETE' });
  if (res.ok) { toast('PLAYLIST REMOVED'); afterPlaylistChange(); }
  else toast('DELETE FAILED', true);
}

let iptvChannels = [];
async function loadIPTVList(id, card) {
  const box = document.getElementById('iptv-browse');
  if (!box) return;
  if (card) {
    document.querySelectorAll('.list-grid .card').forEach(x => x.classList.remove('selected'));
    card.classList.add('selected');
  }
  box.innerHTML = '<div class="loading">LOADING CHANNELS…</div>';
  const chans = (await fetchJSON('/api/iptv/channels?list=' + id + '&limit=20000')) || [];
  iptvChannels = chans;
  const groups = {};
  for (const ch of chans) {
    const g = ch.group || 'Ungrouped';
    (groups[g] = groups[g] || []).push(ch);
  }
  const keys = Object.keys(groups).sort();
  const groupSel = keys.length > 1
    ? '<option value="">All groups (' + chans.length + ')</option>' +
      keys.map(g => '<option value="' + escAttr(g) + '">' + esc(g) + ' (' + groups[g].length + ')</option>').join('')
    : '';
  box.innerHTML =
    '<div class="sect"><h4>Channels · ' + chans.length + '</h4>' +
      '<div class="toolbar">' +
        '<input id="iptv-q" type="search" placeholder="Search channels…" autocomplete="off">' +
        (groupSel ? '<select id="iptv-group" class="sel">' + groupSel + '</select>' : '') +
      '</div>' +
      '<div id="ch-grid" class="ch-grid"></div>' +
    '</div>';
  const gridEl = document.getElementById('ch-grid');
  const qEl = document.getElementById('iptv-q');
  const gEl = document.getElementById('iptv-group');
  function render() {
    const g = gEl ? gEl.value : '';
    const q = (qEl.value || '').toLowerCase();
    const src = g ? (groups[g] || []) : chans;
    const out = q ? src.filter(ch => (ch.name || '').toLowerCase().indexOf(q) !== -1) : src;
    gridEl.innerHTML = out.slice(0, 400).map(chCard).join('') +
      (out.length > 400 ? '<div class="hint-line">Showing 400 of ' + out.length + ' — refine your search</div>' : '') ||
      '<div class="empty-state small"><p>No channels match.</p></div>';
  }
  render();
  qEl.addEventListener('input', debounce(render, 150));
  if (gEl) gEl.addEventListener('change', render);
}

function chCard(ch) {
  const name = ch.name || 'Live';
  const logo = ch.logo
    ? '<img src="' + escAttr(ch.logo) + '" alt="" loading="lazy" onerror="this.remove()">'
    : '';
  return '<div class="ch-card" data-act="channel" data-url="' + escAttr(ch.url) + '" data-name="' + escAttr(name) + '">' +
    '<div class="ch-logo">' + logo + '<span class="ch-fallback">' + esc(initial(name)) + '</span></div>' +
    '<div class="ch-name">' + esc(name) + '</div>' +
  '</div>';
}

function openStream(title, url) {
  pausePlayer();
  document.getElementById('player-title').textContent = title || 'Live';
  const v = document.getElementById('player');
  if (url.lastIndexOf('.m3u8') !== -1 || url.indexOf('.m3u8?') !== -1) {
    if (window.Hls && Hls.isSupported()) {
      window._hls = new Hls({ manifestLoadingTimeOut: 15000 });
      window._hls.attachMedia(v);
      window._hls.loadSource(url);
      window._hls.on(Hls.Events.MANIFEST_PARSED, function () { v.play().catch(function () {}); });
      window._hls.on(Hls.Events.ERROR, function (ev, data) {
        if (data.fatal) toast('STREAM ERROR — channel may be offline', true);
      });
    } else if (v.canPlayType('application/vnd.apple.mpegurl')) {
      v.src = url;
      v.play().catch(function () {});
    } else {
      toast('HLS NOT SUPPORTED BY BROWSER', true);
      return;
    }
  } else {
    v.src = url;
    v.play().catch(function () {});
  }
  document.getElementById('player-modal').classList.remove('hidden');
}

function debounce(fn, ms) {
  let t = null;
  return function () {
    const args = arguments;
    clearTimeout(t);
    t = setTimeout(function () { fn.apply(null, args); }, ms);
  };
}

/* ------------ search page ------------ */
function pageSearch(c) {
  c.innerHTML =
    '<div class="search-bar">' +
      '<input type="search" id="search-input" placeholder="Search everything…" autocomplete="off">' +
      '<span class="search-ic">/</span>' +
    '</div>' +
    '<div id="search-results">' + searchHint('Type at least 2 characters to search every library.') + '</div>';
  const input = document.getElementById('search-input');
  input.addEventListener('input', function () { doSearch(this.value); });
  input.focus();
}

function searchHint(msg) {
  return '<div class="empty-state"><div class="empty-state-icon">&#8981;</div><p>' + esc(msg) + '</p></div>';
}

let searchTimer = null;
function doSearch(q) {
  clearTimeout(searchTimer);
  const box = document.getElementById('search-results');
  if (!q || q.trim().length < 2) {
    box.innerHTML = searchHint('Type at least 2 characters to search every library.');
    return;
  }
  searchTimer = setTimeout(async function () {
    try {
      const items = await fetchJSON('/api/search?q=' + encodeURIComponent(q.trim()));
      if (!document.getElementById('search-results')) return;
      box.innerHTML = items.length
        ? '<div class="result-count">' + items.length + ' result' + (items.length === 1 ? '' : 's') + '</div>' +
          '<div class="media-grid">' + items.map(mediaCard).join('') + '</div>'
        : searchHint('No matches for "' + q.trim() + '".');
    } catch (err) {
      box.innerHTML = searchHint(err.message);
    }
  }, 250);
}

/* ------------ media cards ------------ */
function hueOf(s) {
  let h = 0;
  for (let i = 0; i < (s || '').length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

function thumbStyle(seed) {
  const h = hueOf(seed || '?');
  return 'background:' +
    'radial-gradient(circle at 25% 15%, hsla(' + h + ',85%,60%,.28), transparent 60%),' +
    'radial-gradient(circle at 85% 95%, hsla(' + ((h + 60) % 360) + ',80%,55%,.18), transparent 55%),' +
    'linear-gradient(160deg, #0d1322, #070a12)';
}

// Poster art sits over the gradient; if the image fails it removes itself.
// Resume bar shown on cards with saved progress.
function resumeBar(m) {
  const pct = m.progress_pct || 0;
  return pct > 0 ? '<div class="resume-bar"><div class="resume-fill" style="width:' + pct + '%"></div></div>' : '';
}

function posterImg(m) {
  return m.poster_id
    ? '<img class="poster" alt="" loading="lazy" src="/api/poster/' + m.poster_id + '" onerror="this.remove()">'
    : '';
}

function initial(title) {
  const t = (title || '').trim();
  return t ? t.charAt(0).toUpperCase() : '?';
}

function epLabel(e) {
  const s = e.season > 0 ? e.season : 1;
  const n = e.episode > 0 ? e.episode : 0;
  return 'S' + String(s).padStart(2, '0') + (n ? ' · E' + String(n).padStart(2, '0') : '');
}

function mediaCard(m) {
  const isEp = m.season !== null && m.season !== undefined && m.season > 0;
  return '<div class="media-card' + (m.audio ? ' audio-card' : '') + '" data-act="media" data-id="' + m.id + '" data-title="' + escAttr(m.title) + '">' +
    '<div class="media-thumb" style="' + thumbStyle(m.title) + '">' +
      posterImg(m) +
      '<span class="thumb-init">' + esc(initial(m.title)) + '</span>' +
      '<div class="play-glyph' + (m.audio ? ' audio' : '') + '">' + (m.audio ? '&#9834;' : '&#9654;') + '</div>' +
      '<button class="info-btn" data-act="details" data-id="' + m.id + '" title="Details">i</button>' +
      '<span class="media-badge' + (isEp ? ' ep' : '') + '">' + (isEp ? esc(epLabel(m)) : esc((m.ext || '').toUpperCase().slice(1))) + '</span>' +
      (m.is_usb ? '<span class="media-usb-tag">USB</span>' : '') +
    '</div>' +
    resumeBar(m) +
    '<div class="media-info">' +
      '<div class="media-title">' + esc(m.title) + '</div>' +
      '<div class="media-meta">' + esc(m.library || '') + ' · ' + fmtSize(m.size) + (m.year ? ' · ' + m.year : '') +
        (m.progress_pct ? ' · ' + m.progress_pct + '%' : '') + '</div>' +
    '</div>' +
  '</div>';
}

function episodeCard(e, i) {
  return '<div class="media-card ep-card" data-act="media" data-id="' + e.id + '" data-title="' + escAttr(e.title) + '">' +
    '<div class="media-thumb" style="' + thumbStyle(e.title) + '">' +
      posterImg(e) +
      '<div class="play-glyph">&#9654;</div>' +
      '<button class="info-btn" data-act="details" data-id="' + e.id + '" title="Details">i</button>' +
      '<span class="media-badge ep">' + esc(epLabel(e.episode ? e : { season: e.season, episode: e.episode || i + 1 })) + '</span>' +
    '</div>' +
    '<div class="media-info">' +
      resumeBar(e) +
      '<div class="media-title">' + esc(e.title) + '</div>' +
      '<div class="media-meta">' + fmtSize(e.size) + ' · ' + esc((e.ext || '').toUpperCase().slice(1)) +
        (e.progress_pct ? ' · ' + e.progress_pct + '%' : '') + '</div>' +
    '</div>' +
  '</div>';
}

/* ------------ library view ------------ */
let libState = null;

async function openLibrary(id, name) {
  const c = document.getElementById('page-content');
  document.getElementById('page-title').textContent = (name || 'Library').toUpperCase();
  document.getElementById('page-sub').textContent = 'Movies play directly · shows open their episode list';
  c.innerHTML = '<div class="loading">OPENING LIBRARY…</div>';
  const items = await fetchJSON('/api/media/' + id);
  const movies = items.filter(i => i.type !== 'show' && !i.audio).length;
  const shows = items.filter(i => i.type === 'show').length;
  libState = { id: id, name: name, items: items, filter: 'all', sort: 'az' };

  c.innerHTML =
    '<div class="lib-head">' +
      '<button class="btn btn-ghost" data-act="back-libs">&larr; LIBRARIES</button>' +
    '<button class="btn btn-small" data-act="rescan-lib" data-id="' + id + '">RESCAN</button>' +
      '<div class="chips">' +
        '<span class="stat-chip"><b>' + movies + '</b><span>MOVIES</span></span>' +
        '<span class="stat-chip"><b>' + shows + '</b><span>SERIES</span></span>' +
        '<span class="stat-chip"><b>' + items.filter(i => i.audio).length + '</b><span>TRACKS</span></span>' +
      '</div>' +
      '<div class="lib-controls">' +
        '<div class="seg" role="tablist">' +
          segBtn('all', 'ALL') + segBtn('video', 'VIDEO') + segBtn('shows', 'SERIES') + segBtn('music', 'MUSIC') +
        '</div>' +
        '<select id="lib-sort" class="sel">' +
          '<option value="az">Title A–Z</option>' +
          '<option value="new">Newest</option>' +
          '<option value="old">Oldest</option>' +
          '<option value="size">Largest</option>' +
        '</select>' +
      '</div>' +
    '</div>' +
    '<div id="lib-body"></div>';

  document.getElementById('lib-sort').addEventListener('change', function () {
    libState.sort = this.value;
    drawLibrary();
  });
  drawLibrary();
}

function segBtn(key, label) {
  return '<button class="seg-btn' + (libState.filter === key ? ' active' : '') + '" data-act="lib-filter" data-filter="' + key + '">' + label + '</button>';
}

function setLibFilter(f) {
  if (!libState) return;
  libState.filter = f;
  document.querySelectorAll('.seg-btn').forEach(b => b.classList.toggle('active', b.dataset.filter === f));
  drawLibrary();
}

function drawLibrary() {
  const body = document.getElementById('lib-body');
  if (!body || !libState) return;
  let items = libState.items.slice();
  if (libState.filter === 'video') items = items.filter(i => i.type !== 'show' && !i.audio);
  if (libState.filter === 'music') items = items.filter(i => i.audio);
  if (libState.filter === 'shows') items = items.filter(i => i.type === 'show');
  const s = libState.sort;
  if (s === 'az') items.sort((a, b) => (a.title || '').localeCompare(b.title || ''));
  else if (s === 'new') items.sort((a, b) => (b.added_at || '').localeCompare(a.added_at || ''));
  else if (s === 'old') items.sort((a, b) => (a.added_at || '').localeCompare(b.added_at || ''));
  else if (s === 'size') items.sort((a, b) => (b.size || 0) - (a.size || 0));

  body.innerHTML = items.length
    ? '<div class="media-grid">' + items.map(libraryEntryCard).join('') + '</div>'
    : empty('Nothing here yet.', libState.items.length ? 'Try another filter' : 'Add video files or run a scan');
}

// Movies play; shows open their episode browser — never dump raw episodes
// into the library grid.
function libraryEntryCard(m) {
  if (m.type === 'show') {
    return '<div class="media-card show-card" data-act="show" data-lib="' + m.library_id +
      '" data-folder="' + escAttr(m.path) + '" data-title="' + escAttr(m.title) + '" data-lib-name="' + escAttr(libState.name) + '">' +
      '<div class="media-thumb" style="' + thumbStyle(m.title) + '">' +
        posterImg(m) +
        '<span class="thumb-init">' + esc(initial(m.title)) + '</span>' +
        '<div class="play-glyph">&#9635;</div>' +
        '<span class="media-badge ep">' + (m.episode || 0) + ' EP</span>' +
        '<span class="stack-tag">SERIES</span>' +
        (m.is_usb ? '<span class="media-usb-tag">USB</span>' : '') +
      '</div>' +
      '<div class="media-info">' +
        '<div class="media-title">' + esc(m.title) + '</div>' +
        '<div class="media-meta">TV series · ' + (m.episode || 0) + ' episodes</div>' +
      '</div>' +
    '</div>';
  }
  return mediaCard(m);
}

async function openShow(libId, folder, title, libName) {
  const c = document.getElementById('page-content');
  document.getElementById('page-title').textContent = (title || 'Series').toUpperCase();
  document.getElementById('page-sub').textContent = 'Episodes grouped by season';
  c.innerHTML = '<div class="loading">LOADING EPISODES…</div>';
  const eps = await fetchJSON('/api/media/' + libId + '?folder=' + encodeURIComponent(folder));

  const seasons = {};
  eps.forEach(function (ep) {
    const s = ep.season > 0 ? ep.season : 1;
    (seasons[s] = seasons[s] || []).push(ep);
  });
  const keys = Object.keys(seasons).map(Number).sort((a, b) => a - b);
  let html =
    '<div class="lib-head">' +
      '<button class="btn btn-ghost" data-act="library" data-id="' + libId + '" data-name="' + escAttr(libName || '') + '">&larr; ' + esc((libName || 'LIBRARY').toUpperCase()) + '</button>' +
      '<div class="chips"><span class="stat-chip"><b>' + keys.length + '</b><span>SEASONS</span></span>' +
      '<span class="stat-chip"><b>' + eps.length + '</b><span>EPISODES</span></span></div>' +
    '</div>';
  keys.forEach(function (k) {
    html += '<div class="section-label">SEASON ' + String(k).padStart(2, '0') + '</div><div class="media-grid">';
    seasons[k].forEach(function (e, i) { html += episodeCard(e, i); });
    html += '</div>';
  });
  c.innerHTML = html;
}

/* ------------ player ------------ */
function openMedia(id, title, opts) {
  pausePlayer();
  stopCompatStream();
  const qi = playQueue.findIndex(function (q) { return q.id === id; });
  if (qi >= 0) queueIndex = qi;
  renderQueue();
  currentMediaId = id;
  currentTitle = title || 'Now playing';
  document.getElementById('player-compat').classList.add('hidden');
  const m = document.getElementById('player-modal');
  const v = document.getElementById('player');
  m.classList.remove('hidden');
  document.getElementById('player-title').textContent = title || 'Now playing';

  v.src = API + '/api/stream/' + id;
  let startAt = -1;
  // Resume where the user left off (only file media, never IPTV).
  fetchJSON('/api/progress/' + id).then(function (p) {
    if (p && p.position && p.duration && p.position < p.duration * 0.95 && p.position > 10) {
      startAt = p.position;
      castStartAt = p.position; // so casting resumes where the browser would
    }
  }).catch(function () {});

  v.play().catch(function () {});
  if (castSession) { setTimeout(function () { loadCastMedia(buildCastMedia()); }, 400); }
  nextEpisode = null;
  fetchJSON('/api/next/' + id).then(function (n) {
    nextEpisode = n && n.next ? n.next : null;
    const btn = document.getElementById('player-next');
    if (btn) btn.classList.toggle('hidden', !nextEpisode);
  }).catch(function () {});
  castPoster = '';
  castSubs = [];
  castStartAt = 0;
  fetchJSON('/api/details/' + id).then(function (d) {
    castPoster = d.poster_id ? ('/api/poster/' + d.poster_id) : '';
    castSubs = d.subtitles || [];
    castLibId = d.library_id || 0;
    v.classList.toggle('audio-mode', !!d.audio);
    if (d.audio) v.poster = d.poster_id ? (API + '/api/poster/' + d.poster_id) : '';
    else v.removeAttribute('poster');
    attachSubtitles(d.subtitles || []);
    if (!d.browser_ok) {
      document.getElementById('player-compat').classList.toggle('hidden', !d.compat_possible);
      toast(d.compat_possible ? 'THIS FORMAT MAY NOT PLAY HERE — TRY COMPATIBILITY STREAM' : 'THIS FORMAT MAY NOT PLAY IN THIS BROWSER', true);
    }
    if (opts && opts.compat) startCompat();
  }).catch(function () {});
  v.removeEventListener('timeupdate', saveProgress);
  v.addEventListener('timeupdate', saveProgress);
  v.removeEventListener('ended', onEnded);
  v.addEventListener('ended', onEnded);
  v.removeEventListener('loadedmetadata', resumeFrom);
  v.addEventListener('loadedmetadata', resumeFrom);
  function resumeFrom() {
    if (startAt > 0) {
      v.currentTime = startAt;
      startAt = -1;
    }
  }
}

function onEnded() {
  saveProgress();
  // The queue wins when something was explicitly lined up next.
  if (queueIndex >= 0 && queueIndex + 1 < playQueue.length) {
    const nxt = playQueue[queueIndex + 1];
    setTimeout(queueAdvance, 500);
    toast('NEXT: ' + (nxt.title || '').slice(0, 48));
    return;
  }
  if (!autoNext || !nextEpisode) return;
  const nxt = nextEpisode;
  setTimeout(function () { if (nextEpisode === nxt) openMedia(nxt.id, nxt.title); }, 900);
  toast('NEXT: ' + (nxt.title || '').slice(0, 48));
}

function playNext() {
  if (!nextEpisode) return;
  openMedia(nextEpisode.id, nextEpisode.title);
}

function setSpeed(rate) {
  const v = document.getElementById('player');
  v.playbackRate = rate;
  document.querySelectorAll('.spd-btn').forEach(b =>
    b.classList.toggle('active', Number(b.dataset.rate) === rate));
  toast('SPEED ' + rate + 'x');
}

function saveProgress() {
  const v = document.getElementById('player');
  if (!v.duration || !currentMediaId) return;
  fetch(API + '/api/progress/' + currentMediaId, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      position: v.currentTime,
      duration: v.duration,
      completed: v.currentTime / v.duration > 0.95
    })
  }).catch(function () {});
}

function attachSubtitles(subs) {
  const v = document.getElementById('player');
  v.querySelectorAll('track').forEach(function (t) { t.remove(); });
  subs.forEach(function (s, i) {
    const t = document.createElement('track');
    t.kind = 'subtitles';
    t.label = s.label;
    t.src = API + s.src;
    t.srclang = 'und';
    if (i === 0) t.default = true;
    v.appendChild(t);
  });
}

async function startCompat() {
  if (!currentMediaId) return;
  toast('PREPARING COMPATIBILITY STREAM…');
  const res = await fetch(API + '/api/compat/' + currentMediaId, { method: 'POST' });
  if (!res.ok) { toast(await errorText(res, 'COMPATIBILITY STREAM FAILED'), true); return; }
  const d = await res.json();
  compatActive = true;
  openStream(currentTitle, d.url);
  document.getElementById('player-modal').classList.remove('hidden');
}

function stopCompatStream() {
  if (compatActive && currentMediaId) {
    fetch(API + '/api/compat/' + currentMediaId, { method: 'DELETE' }).catch(function () {});
  }
  compatActive = false;
}

/* ------------ details ------------ */
async function openDetails(id) {
  const modal = document.getElementById('detail-modal');
  const body = document.getElementById('detail-body');
  modal.classList.remove('hidden');
  body.innerHTML = '<div class="loading">LOADING…</div>';
  let d;
  try { d = await fetchJSON('/api/details/' + id); }
  catch (err) { body.innerHTML = empty('Could not open this title', err.message); return; }

  const isEp = d.season > 0 || d.episode > 0;
  const facts = [];
  if (d.year) facts.push(d.year);
  if (d.rating) facts.push('★ ' + d.rating.toFixed(1));
  if (d.duration) facts.push(fmtTime(d.duration));
  facts.push(fmtSize(d.size));
  facts.push((d.ext || '').slice(1).toUpperCase());
  if (d.audio && d.audio_codec) facts.push(d.audio_codec.toUpperCase());
  else if (d.video_codec) facts.push(d.video_codec.toUpperCase() + (d.audio_codec ? ' / ' + d.audio_codec.toUpperCase() : ''));
  const subs = (d.subtitles || []).map(function (s) { return esc(s.label); });

  const art = d.poster_id
    ? '<img class="detail-art" alt="" src="/api/poster/' + d.poster_id + '" onerror="this.remove()">'
    : '<div class="detail-art detail-placeholder" style="' + thumbStyle(d.title) + '"><span>' + esc(initial(d.title)) + '</span></div>';
  const subsFound = (d.subtitles || []).length < 3;

  body.innerHTML =
    '<button class="btn btn-ghost btn-small detail-close" data-act="close-detail">ESC ✕</button>' +
    '<div class="detail-grid">' +
      '<div class="detail-poster">' + art + '</div>' +
      '<div class="detail-info">' +
        '<div class="eyebrow">' + (d.audio ? 'AUDIO TRACK' : isEp ? 'EPISODE · S' + String(d.season || 1).padStart(2, '0') + ' E' + String(d.episode || 0).padStart(2, '0') : 'MOVIE') + ' · ' + esc(d.library) + (d.is_usb ? ' · USB' : '') + '</div>' +
        '<h2>' + esc(d.title) + '</h2>' +
        '<div class="detail-facts">' + facts.map(function (f) { return '<span>' + esc(f) + '</span>'; }).join('') + '</div>' +
        '<p class="detail-over">' + (d.overview ? esc(d.overview) : 'No synopsis yet. Turn on online artwork in Settings to fetch one.') + '</p>' +
        '<div class="detail-actions">' +
          '<button class="btn btn-primary" data-act="media" data-id="' + d.id + '" data-title="' + escAttr(d.title) + '">▶ PLAY</button>' +
          (!d.browser_ok && d.compat_possible
            ? '<button class="btn" data-act="compat">COMPATIBILITY STREAM</button>' : '') +
          '<button class="btn btn-ghost" data-act="mark" data-id="' + d.id + '">MARK WATCHED</button>' +
          '<button class="btn btn-ghost" data-act="forget" data-id="' + d.id + '">FORGET</button>' +
          (currentUser && currentUser.role === 'admin'
            ? '<button class="btn btn-ghost" data-act="edit-meta" data-id="' + d.id + '">EDIT INFO</button>' : '') +
          '<button class="btn btn-ghost" data-act="fav" data-id="' + d.id + '" id="fav-btn">' +
            (favSet[d.id] ? '&#9733; IN MY LIST' : '&#9734; ADD TO MY LIST') + '</button>' +
          '<button class="btn btn-ghost" data-act="queue-add" data-id="' + d.id + '" data-title="' + escAttr(d.title) + '">+ QUEUE</button>' +
        '</div>' +
        (!d.browser_ok
          ? '<p class="hint-line warn">This file uses a format this browser may not play' +
            (d.compat_possible ? ' — the compatibility stream converts it on the fly.' : '. Install ffmpeg on the server to convert it.') + '</p>'
          : '') +
        '<div class="detail-sub"><span class="label">SUBTITLES</span> ' + (subs.length ? subs.join(' · ') : 'none found') +
          ' <span class="hint-line">(place Movie.srt beside Movie.mp4)</span>' +
          (subsFound
            ? ' <button class="btn btn-small btn-ghost" data-act="find-subs" data-id="' + d.id + '">FIND MORE ONLINE</button>'
            : '') +
        '</div>' +
        '<div id="sub-results"></div>' +
        '<div id="meta-editor"></div>' +
        '<div class="detail-path">' + esc(d.path) + '</div>' +
      '</div>' +
    '</div>';
}

function closeHelp() {
  document.getElementById('help-modal').classList.add('hidden');
}

function closeDetail() {
  document.getElementById('detail-modal').classList.add('hidden');
  document.getElementById('detail-body').innerHTML = '';
}

// Mark watched: record progress for the whole file so the title leaves the
// Continue Watching rail.
async function markWatched(id) {
  const res = await fetch(API + '/api/progress/' + id, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ position: 9999, duration: 10000, completed: true })
  });
  if (res.ok) { toast('MARKED AS WATCHED'); if (currentMediaId === id) closePlayer(); closeDetail(); if (currentPage === 'home') loadPage('home'); }
  else toast('COULD NOT SAVE PROGRESS', true);
}

async function rescanLibrary(id) {
  toast('RESCANNING…');
  const res = await fetch(API + '/api/libraries/' + id, { method: 'POST' });
  if (!res.ok) { toast('RESCAN FAILED', true); return; }
  watchScan(function () { loadPage(currentPage); });
}

async function cancelDownload(id) {
  const res = await fetch(API + '/api/ytdlp/cancel/' + id, { method: 'DELETE' });
  toast(res.ok ? 'DOWNLOAD STOPPED' : 'COULD NOT STOP', !res.ok);
  pollDownloads();
}

async function forgetMedia(id) {
  const res = await fetch(API + '/api/progress/' + id, { method: 'DELETE' });
  if (res.ok) { toast('REMOVED FROM CONTINUE WATCHING'); closeDetail(); if (currentPage === 'home') loadPage('home'); }
  else toast('COULD NOT REMOVE', true);
}

/* ---------- downloads (yt-dlp) ---------- */
async function pageDownloads(c) {
  const [st, libs] = await Promise.all([
    fetchJSON('/api/ytdlp'),
    fetchJSON('/api/libraries')
  ]);
  let h = '<div class="panel wide">';
  if (!st.available) {
    h += '<div class="notice">yt-dlp is not installed, so downloads are unavailable.<br>' +
      'Install it once with <code>pipx install yt-dlp</code> (or drop the binary anywhere on your PATH). ' +
      'Already have it elsewhere? Set its path in Settings &#8594; Downloads.</div>';
  }
  h += '<div class="sect"><h4>New download</h4>';
  if (!libs.length) {
    h += '<p class="hint-line">No libraries yet &#8212; connect the USB drive first.</p></div>';
  } else {
    const usbIdx = libs.findIndex(l => l.is_usb);
    const dflt = usbIdx >= 0 ? libs[usbIdx].id : libs[0].id;
    h += '<div class="form-grid form-grid-three">' +
      '<input type="url" id="dl-url" placeholder="https://&#8230; video or audio link">' +
      '<select id="dl-lib" class="sel">' + libs.map(l =>
        '<option value="' + l.id + '"' + (l.id === dflt ? ' selected' : '') + '>' +
        esc(l.name) + (l.is_usb ? ' [USB]' : '') + '</option>').join('') + '</select>' +
      '<button class="btn btn-primary" id="btn-dl">DOWNLOAD</button></div>' +
      '<div class="form-grid form-grid-four" style="margin-top:10px">' +
        '<select id="dl-kind" class="sel">' +
          '<option value="video">Video</option><option value="audio">Audio only</option></select>' +
        '<select id="dl-quality" class="sel">' +
          '<option value="best">Best quality</option>' +
          '<option value="1080">Up to 1080p</option>' +
          '<option value="720">Up to 720p</option>' +
          '<option value="480">Up to 480p</option></select>' +
        '<select id="dl-audio" class="sel">' +
          '<option value="mp3">MP3</option><option value="m4a">M4A</option>' +
          '<option value="opus">Opus</option><option value="flac">FLAC</option>' +
          '<option value="wav">WAV</option></select>' +
        '<select id="dl-bitrate" class="sel">' +
          '<option value="0">Best bitrate</option><option value="320">320 kbps</option>' +
          '<option value="192">192 kbps</option><option value="128">128 kbps</option></select>' +
      '</div>' +
      '<div class="form-grid form-grid-four" style="margin-top:10px;align-items:center">' +
        '<input type="text" id="dl-subdir" placeholder="Folder (optional, e.g. My Show)">' +
        '<label class="chk"><input type="checkbox" id="dl-playlist"> Whole playlist</label>' +
        '<label class="chk"><input type="checkbox" id="dl-subs"> Download subtitles</label>' +
      '</div>' +
      '<p class="hint-line">Files are written straight onto the selected library and indexed when the download finishes. ' +
        'Only download content you are allowed to download.</p></div>';
  }
  h += '<div class="sect"><h4>Queue</h4>' +
    '<div id="dl-queued" class="dl-queued"></div>' +
    '<div id="dl-rows"></div></div></div>';
  c.innerHTML = h;
  const go = document.getElementById('btn-dl');
  if (go) go.addEventListener('click', startDownload);
  const kind = document.getElementById('dl-kind');
  if (kind) {
    const toggleAudio = function () {
      const audio = kind.value === 'audio';
      document.getElementById('dl-audio').disabled = !audio;
      document.getElementById('dl-bitrate').disabled = !audio;
      document.getElementById('dl-quality').disabled = audio;
      document.getElementById('dl-subs').disabled = audio;
    };
    kind.addEventListener('change', toggleAudio);
    toggleAudio();
  }
  pollDownloads();
}

async function startDownload() {
  const url = document.getElementById('dl-url').value.trim();
  if (!url) { toast('PASTE A LINK FIRST', true); return; }
  const kind = document.getElementById('dl-kind').value;
  const body = {
    url: url,
    library: Number(document.getElementById('dl-lib').value),
    kind: kind,
    quality: kind === 'video' ? document.getElementById('dl-quality').value : '',
    audio_fmt: document.getElementById('dl-audio').value,
    bitrate: document.getElementById('dl-bitrate').value,
    subtitles: document.getElementById('dl-subs').checked && kind !== 'audio',
    playlist: document.getElementById('dl-playlist').checked,
    subdir: document.getElementById('dl-subdir').value.trim()
  };
  const res = await postRaw('/api/ytdlp', body);
  if (!res.ok) { toast(await errorText(res, 'COULD NOT START DOWNLOAD'), true); return; }
  document.getElementById('dl-url').value = '';
  toast('DOWNLOAD STARTED');
  pollDownloads(true);
}

let dlTimer = null;
async function pollDownloads() {
  if (currentPage !== 'downloads') return;
  const box = document.getElementById('dl-rows');
  if (!box) return;
  let jobs = [];
  try { jobs = (await fetchJSON('/api/ytdlp/jobs')) || []; } catch (e) { return; }
  box.innerHTML = jobs.length
    ? jobs.map(dlRow).join('')
    : '<p class="hint-line">Nothing downloading yet.</p>';
  const active = jobs.some(j => j.status === 'downloading');
  const queued = jobs.filter(j => j.status === 'queued').length;
  const qEl = document.getElementById('dl-queued');
  if (qEl) qEl.textContent = queued ? queued + ' waiting for a free slot' : '';
  clearTimeout(dlTimer);
  if (active) dlTimer = setTimeout(pollDownloads, 1200);
}

function dlRow(j) {
  if (j.status === 'queued') {
    return '<div class="dl-row queued">' +
      '<div class="dl-main">' +
        '<div class="dl-title">' + esc(j.title || j.url || 'download') + '</div>' +
        '<div class="dl-meta"><span>WAITING FOR A FREE SLOT</span><span class="dl-state">QUEUED</span></div>' +
      '</div>' +
      '<button class="btn btn-danger btn-small" data-act="dl-cancel" data-id="' + j.id + '">STOP</button>' +
    '</div>';
  }
  const pct = j.percent > 0 ? Math.round(j.percent) : 0;
  const indeterminate = j.percent < 0;
  let meta = [];
  if (j.status === 'downloading') {
    if (j.total) meta.push(pct + '%');
    if (j.speed) meta.push((j.speed / 1048576).toFixed(1) + ' MB/s');
    if (j.bytes && j.total) meta.push(fmtSize(j.bytes) + ' / ' + fmtSize(j.total));
    if (j.eta) meta.push('ETA ' + fmtTime(j.eta));
  } else if (j.status === 'finished') {
    meta.push(j.file ? esc(j.file.split('/').pop()) : 'done');
  } else if (j.status === 'error') {
    meta.push(esc(j.error || 'failed'));
  }
  return '<div class="dl-row ' + esc(j.status) + '">' +
    '<div class="dl-main">' +
      '<div class="dl-title">' + esc(j.title || j.url || 'download') + '</div>' +
      '<div class="dl-meta">' + meta.map(m => '<span>' + m + '</span>').join('') +
        ' <span class="dl-state">' + esc(String(j.status).toUpperCase()) + '</span></div>' +
    '</div>' +
    '<div class="dl-bar"><div class="dl-fill' + (indeterminate ? ' ind' : '') + '" style="width:' +
      (indeterminate ? 100 : Math.max(2, Math.min(100, pct))) + '%"></div></div>' +
    (j.status === 'downloading'
      ? '<button class="btn btn-danger btn-small" data-act="dl-cancel" data-id="' + j.id + '">STOP</button>' : '') +
  '</div>';
}

/* ---------- duplicates ---------- */
async function pageDuplicates(c) {
  c.innerHTML = '<div class="loading">COMPARING TITLES…</div>';
  const groups = (await fetchJSON('/api/duplicates')) || [];
  let html = '<div class="panel wide">';
  if (!groups.length) {
    html += empty('No duplicates found.', 'Every title in your libraries appears once');
  } else {
    html += '<div class="notice">' + groups.length + ' title(s) appear more than once. ' +
      'These may be different films that share a name — check before deleting anything.</div>';
    for (const g of groups) {
      html += '<div class="sect"><h4>' + esc(g.title) + ' · ' + g.copies + ' copies</h4>';
      html += '<div class="dup-grid">' + g.items.map(function (i) {
        return '<div class="dup-row" data-act="media" data-id="' + i.id + '" data-title="' + escAttr(i.title) + '">' +
          '<div class="dup-name">' + esc(i.title) + '</div>' +
          '<div class="dup-path">' + esc(i.path) + '</div>' +
          '<div class="dup-meta">' + fmtSize(i.size) + (i.year ? ' · ' + i.year : '') +
            ' · ' + esc(i.library) + (i.is_usb ? ' · USB' : '') + '</div>' +
        '</div>';
      }).join('') + '</div></div>';
    }
  }
  html += '</div>';
  c.innerHTML = html;
}

/* ---------- metadata override (admin) ---------- */
let metaPick = null;
const favSet = {};      // media id -> true when starred

function refreshFavButtons() {
  const btn = document.getElementById('fav-btn');
  if (!btn) return;
  const id = Number(btn.dataset.id || 0);
  btn.innerHTML = favSet[id] ? '&#9733; IN MY LIST' : '&#9734; ADD TO MY LIST';
}

async function toggleFav(id) {
  const method = favSet[id] ? 'DELETE' : 'POST';
  const res = await fetch(API + '/api/favorites/' + id, { method: method });
  if (!res.ok) { toast('COULD NOT UPDATE MY LIST', true); return; }
  favSet[id] = method === 'POST';
  refreshFavButtons();
  toast(favSet[id] ? 'ADDED TO MY LIST' : 'REMOVED FROM MY LIST');
  if (currentPage === 'home') loadPage('home');
}

/* ---------- playback queue ---------- */
function queueSave() {
  try { localStorage.setItem(QUEUE_KEY, JSON.stringify(playQueue)); } catch (e) {}
}

function queueAdd(id, title) {
  if (playQueue.some(q => q.id === id)) { toast('ALREADY IN QUEUE', true); return; }
  playQueue.push({ id: id, title: title || '' });
  queueSave();
  renderQueue();
  toast('ADDED TO QUEUE');
}

function queueClear() {
  playQueue = [];
  queueIndex = -1;
  queueSave();
  renderQueue();
  toast('QUEUE CLEARED');
}

function queuePlayIndex(i) {
  if (i < 0 || i >= playQueue.length) return;
  queueIndex = i;
  const item = playQueue[i];
  openMedia(item.id, item.title);
}

function queueAdvance() {
  if (queueIndex < 0) return;
  if (queueIndex + 1 < playQueue.length) {
    queuePlayIndex(queueIndex + 1);
  } else {
    queueIndex = -1;
    renderQueue();
  }
}

function renderQueue() {
  const el = document.getElementById('queue-panel');
  if (!el) return;
  el.innerHTML = playQueue.length
    ? '<div class="q-head">QUEUE <span>' + playQueue.length + '</span>' +
        '<button class="btn btn-ghost btn-small" data-act="queue-clear">CLEAR</button></div>' +
      playQueue.map(function (q, i) {
        return '<div class="q-row' + (i === queueIndex ? ' active' : '') + '" data-act="queue-play" data-idx="' + i + '">' +
          '<span class="q-n">' + (i + 1) + '</span><span class="q-t">' + esc(q.title || ('item ' + q.id)) + '</span></div>';
      }).join('')
    : '<div class="q-head">QUEUE <span>empty</span></div>';
}

async function openMetaEditor(id) {
  const box = document.getElementById('meta-editor');
  if (!box) return;
  metaPick = null;
  let d;
  try { d = await fetchJSON('/api/details/' + id); } catch (e) { box.innerHTML = empty('Could not load', e.message); return; }
  box.innerHTML =
    '<div class="meta-box">' +
      '<div class="meta-head"><h4>EDIT INFO</h4>' +
        '<button class="btn btn-ghost btn-small" data-act="close-meta">✕</button></div>' +
      '<label class="meta-label">Title</label>' +
      '<input type="text" id="meta-title" value="' + escAttr(d.title) + '">' +
      '<label class="meta-label">Search The Movie Database</label>' +
      '<div class="meta-search">' +
        '<input type="text" id="meta-q" value="' + escAttr(cleanTitleForSearch(d.title)) + '" placeholder="Title to look up">' +
        '<button class="btn btn-small" data-act="search-meta" data-id="' + id + '">SEARCH</button>' +
      '</div>' +
      '<div id="meta-results" class="meta-results"></div>' +
      '<div class="btn-row"><button class="btn btn-primary" data-act="save-meta" data-id="' + id + '">SAVE</button></div>' +
      '<p class="hint-line">Clicking a result sets its poster, synopsis and rating. The title is only changed by SAVE.</p>' +
    '</div>';
}

function cleanTitleForSearch(t) {
  return (t || '')
    .replace(/full\s+(hd\s+)?(movie\s+)?online.*$/i, '')
    .replace(/myflix.*$/i, '')
    .replace(/\s+/g, ' ')
    .trim();
}

async function searchMeta(id) {
  const q = document.getElementById('meta-q').value.trim();
  if (!q) { toast('TYPE A TITLE TO SEARCH', true); return; }
  const box = document.getElementById('meta-results');
  box.innerHTML = '<div class="loading small">SEARCHING…</div>';
  let d;
  try {
    d = await fetchJSON('/api/metadata/search?media_id=' + id + '&q=' + encodeURIComponent(q));
  } catch (e) {
    box.innerHTML = empty('Search failed', e.message);
    return;
  }
  if (!d.results || !d.results.length) { box.innerHTML = empty('No matches', 'Try a different title'); return; }
  box.innerHTML = d.results.map(function (r) {
    const img = r.poster_url
      ? '<img src="' + escAttr(r.poster_url) + '" alt="" loading="lazy" onerror="this.remove()">'
      : '<div class="meta-thumb-none"></div>';
    return '<div class="meta-pick" data-act="apply-meta" data-id="' + id + '" ' +
      'data-overview="' + escAttr(r.overview || '') + '" data-rating="' + (r.rating || 0) + '" ' +
      'data-poster="' + escAttr(r.poster_url || '') + '" data-name="' + escAttr(r.title) + '">' +
      '<div class="meta-thumb">' + img + '</div>' +
      '<div class="meta-name">' + esc(r.title) + (r.year ? ' (' + r.year + ')' : '') + '</div>' +
    '</div>';
  }).join('');
}

// Selecting a result remembers what SAVE should send.
function selectMetaResult(el) {
  document.querySelectorAll('#meta-results .meta-pick').forEach(x => x.classList.remove('picked'));
  el.classList.add('picked');
  metaPick = {
    overview: el.dataset.overview || '',
    rating: parseFloat(el.dataset.rating || '0') || 0,
    poster: el.dataset.poster || '',
  };
  // Show the synopsis immediately so the choice can be sanity-checked.
  const over = document.querySelector('.detail-over');
  if (over && metaPick.overview) over.textContent = metaPick.overview;
}

async function saveMeta(id) {
  const title = document.getElementById('meta-title').value.trim();
  const body = { media_id: id, title: title };
  if (metaPick) {
    body.overview = metaPick.overview;
    body.rating = metaPick.rating;
    body.poster_url = metaPick.poster;
  }
  const res = await postRaw('/api/metadata/apply', body);
  if (res.ok) {
    toast('INFO UPDATED');
    closeDetail();
    loadPage(currentPage);
  } else {
    toast(await errorText(res, 'SAVE FAILED'), true);
  }
}

/* ---------- online subtitle search ---------- */
async function findSubtitles(id) {
  const box = document.getElementById('sub-results');
  if (!box) return;
  box.innerHTML = '<div class="loading small">SEARCHING SUBTITLES…</div>';
  let d;
  try {
    d = await fetchJSON('/api/subtitles/search?media_id=' + id);
  } catch (e) {
    box.innerHTML = empty('Subtitle search unavailable', e.message);
    return;
  }
  if (!d.results || !d.results.length) { box.innerHTML = empty('No subtitles found', ''); return; }
  box.innerHTML = '<div class="sub-list">' + d.results.slice(0, 12).map(function (r) {
    return '<div class="sub-row" data-act="dl-sub" data-id="' + id + '" data-file="' + r.file_id + '">' +
      '<div class="sub-name">' + esc(r.file_name) + '</div>' +
      '<div class="sub-meta">' + esc((r.language || '').toUpperCase()) + (r.movie ? ' · ' + esc(r.movie) : '') + '</div>' +
    '</div>';
  }).join('') + '</div>';
}

async function downloadSubtitle(id, fileId) {
  const res = await postRaw('/api/subtitles/download', { media_id: id, file_id: fileId, file_name: '' });
  if (!res.ok) { toast(await errorText(res, 'DOWNLOAD FAILED'), true); return; }
  const d = await res.json().catch(() => null);
  toast('SUBTITLE SAVED' + (d && d.file ? ' · ' + d.file.split('/').pop() : ''));
  // Reload the details so the new sidecar shows up as a track.
  openDetails(id);
}

/* ---------- settings page ---------- */
async function pageSettings(c) {
  const s = await fetchJSON('/api/settings');
  const admin = currentUser && currentUser.role === 'admin';
  const dis = admin ? '' : ' disabled';
  const toggle = function (id, on) {
    return '<label class="toggle"><input type="checkbox" id="' + id + '"' + (on ? ' checked' : '') + dis + '><span class="track"></span></label>';
  };
  const text = function (id, val, extra) {
    return '<input type="text" id="' + id + '" value="' + escAttr(val || '') + '"' + (extra || '') + dis + '>';
  };
  let html = '<div class="panel wide">';
  if (!admin) html += '<div class="notice">Read-only — only the admin can change server settings.</div>';

  html += '<div class="settings-grid">';
  html += '<div class="sect"><h4>Drives &amp; Libraries</h4>' +
    '<div class="set-row"><div><label>Auto-detect drives</label><small>Watch for USB devices every few seconds</small></div>' +
      toggle('set-autodetect', s.usb_autodetect !== '0') + '</div>' +
    '<div class="set-row col"><div><label>USB mount roots</label><small>Parent folders where drives are mounted, e.g. <code>/media,/mnt,/run/media</code> — NOT the media folders inside a drive</small></div>' +
      text('set-usblib', s.usb_paths || '/media,/mnt,/run/media', ' class="wide-in"') + '</div>' +
    '<div class="set-row col"><div><label>Local media folders</label><small>Folders on this machine to index — comma separated</small></div>' +
      text('set-locals', s.local_paths || '$HOME/Videos,$HOME/Music', ' class="wide-in"') + '</div>' +
    '<div class="set-row col"><div><label>Skip folders containing</label><small>Any path with these words is ignored, e.g. <code>node_modules,.Trash,venv</code></small></div>' +
      text('set-excludes', s.scan_excludes || '', ' class="wide-in" placeholder="node_modules,.Trash"') + '</div>' +
  '</div>';

  html += '<div class="sect"><h4>Scanning</h4>' +
    '<div class="set-row"><div><label>Group show folders</label><small>Folders of episodes become one series card. Flat folders stay as movies.</small></div>' +
      toggle('set-tvgroup', s.tv_grouping !== '0') + '</div>' +
    '<div class="set-row"><div><label>Rescan libraries every (minutes)</label><small>Picks up new files on drives. 0 = manual only</small></div>' +
      '<input type="number" id="set-rescan" min="0" max="1440" value="' + escAttr(s.rescan_minutes || '0') + '"' + dis + '></div>' +
    '<div class="set-row"><div><label>Minimum file size (MB)</label><small>Smaller files are ignored</small></div>' +
      '<input type="number" id="set-minsize" min="1" value="' + escAttr(s.min_size_mb || '1') + '"' + dis + '></div>' +
    '<div class="set-row col"><div><label>Accepted formats</label><small>Comma-separated, with dots</small></div>' +
      text('set-exts', s.media_exts || '.mkv,.mp4,.avi,.mov,.webm,.m4v,.mpg,.mpeg,.flv,.wmv,.ts', ' class="wide-in"') + '</div>' +
  '</div>';

  html += '<div class="sect"><h4>Live TV (IPTV)</h4>' +
    '<div class="set-row col"><div><label>Request user agent</label><small>Some providers only serve player clients</small></div>' +
      text('set-iptv-ua', s.iptv_user_agent || 'VLC/3.0.21 LibVLC/3.0.21', ' class="wide-in"') + '</div>' +
    '<div class="set-row"><div><label>Fetch timeout (seconds)</label><small>5 – 300</small></div>' +
      '<input type="number" id="set-iptv-timeout" min="5" max="300" value="' + escAttr(s.iptv_timeout_sec || '30') + '"' + dis + '></div>' +
    '<div class="set-row"><div><label>Max channels per playlist</label><small>0 means no limit</small></div>' +
      '<input type="number" id="set-iptv-max" min="0" value="' + escAttr(s.iptv_max_channels || '0') + '"' + dis + '></div>' +
    '<div class="set-row"><div><label>Refresh playlists every (hours)</label><small>Re-downloads URL playlists automatically. 0 = manual only</small></div>' +
      '<input type="number" id="set-iptv-refresh" min="0" max="168" value="' + escAttr(s.iptv_refresh_hours || '0') + '"' + dis + '></div>' +
  '</div>';

  html += '<div class="sect art-sect"><h4>Artwork &amp; posters</h4>' +
    '<div class="set-row"><div><label>Look up posters online</label><small>Off by default. Sends only the title and year to The Movie Database.</small></div>' +
      toggle('set-art-online', s.artwork_online === '1') + '</div>' +
    '<div class="set-row col"><div><label>TMDB API key</label><small>Free key from themoviedb.org → Settings → API</small></div>' +
      text('set-tmdb', s.artwork_tmdb_key || '', ' class="wide-in" placeholder="paste key"') + '</div>' +
    (admin
      ? '<div class="art-status" id="art-status">Checking…</div>' +
        '<div class="btn-row"><button class="btn" id="btn-art">FETCH POSTERS NOW</button></div>'
      : '') +
    '<p class="hint-line">Images saved beside a video (Movie.jpg or Movie-poster.jpg) are used automatically, offline.</p>' +
  '</div>';

  html += '<div class="sect"><h4>Casting</h4>' +
    '<div class="set-row"><div><label>Allow casting to a TV</label><small>Adds a CAST button to the player. Loads the Google Cast SDK only while enabled.</small></div>' +
      toggle('set-cast', s.cast_enabled === '1') + '</div>' +
    '<div class="set-row col"><div><label>Server address for the TV</label><small>Leave blank to detect it. Set this if the TV reaches the server on a different address.</small></div>' +
      text('set-cast-url', s.cast_url || '', ' class="wide-in" placeholder="http://192.168.0.99:7676"') + '</div>' +
    '<div class="cast-hint" id="cast-hint">Detecting…</div>' +
  '</div>';

  html += '<div class="sect"><h4>Downloads</h4>' +
    '<div class="set-row"><div><label>Parallel downloads</label><small>1&#8211;5. More can stall a slow drive.</small></div>' +
      '<input type="number" id="set-ytdlp-conc" min="1" max="5" value="' + escAttr(s.ytdlp_concurrency || '2') + '"' + dis + '></div>' +
    '<div class="set-row col"><div><label>yt-dlp path</label><small>Leave blank to use the one on your PATH. Installed? Install once with pipx: <code>pipx install yt-dlp</code></small></div>' +
      text('set-ytdlp', s.ytdlp_path || '', ' class="wide-in" placeholder="yt-dlp (from PATH)"') + '</div>' +
    '<div class="ytdlp-status" id="ytdlp-status">Checking…</div>' +
  '</div>';

  html += '<div class="sect"><h4>Compatibility streaming</h4>' +
    '<div class="set-row col"><div><label>Encoder speed</label><small>Faster uses less CPU, larger files</small></div>' +
      '<select id="set-compat-preset" class="sel wide-in"' + dis + '>' +
        ['ultrafast', 'superfast', 'veryfast', 'faster', 'fast', 'medium', 'slow'].map(function (p) {
          return '<option value="' + p + '"' + (p === (s.compat_preset || 'veryfast') ? ' selected' : '') + '>' + p + '</option>';
        }).join('') + '</select></div>' +
    '<div class="set-row col"><div><label>Maximum height</label><small>0 keeps the original resolution</small></div>' +
      '<select id="set-compat-height" class="sel wide-in"' + dis + '>' +
        [['0', 'Original'], ['1080', '1080p'], ['720', '720p'], ['480', '480p']].map(function (o) {
          return '<option value="' + o[0] + '"' + (o[0] === (s.compat_max_height || '0') ? ' selected' : '') + '>' + o[1] + '</option>';
        }).join('') + '</select></div>' +
  '</div>';

  html += '<div class="sect"><h4>Subtitles</h4>' +
    '<div class="set-row"><div><label>Search subtitles online</label><small>Uses OpenSubtitles. Only searches when you ask.</small></div>' +
      toggle('set-subs-on', s.subs_search_enabled === '1') + '</div>' +
    '<div class="set-row col"><div><label>OpenSubtitles API key</label><small>Free key from opensubtitles.com &#8594; API consumers</small></div>' +
      text('set-subs-key', s.subs_key || '', ' class="wide-in" placeholder="paste key"') + '</div>' +
    '<div class="set-row"><div><label>Language</label><small>Two-letter code</small></div>' +
      '<input type="text" id="set-subs-lang" value="' + escAttr(s.subs_lang || 'en') + '"' + dis + '></div>' +
    '<p class="hint-line">Downloaded subtitles are saved beside the video file, so the drive must be writable.</p>' +
  '</div>';

  html += '<div class="sect"><h4>Server</h4>' +
    '<div class="set-row"><div><label>Data directory</label><small>Index database lives here</small></div><span class="ro-value">' + esc(s.data_dir || 'default') + '</span></div>' +
    '<div class="set-row"><div><label>Listen address</label><small>Changes apply after restart</small></div><span class="ro-value">' + esc(s.addr || ':7676') + '</span></div>' +
    (admin
      ? '<div class="set-row"><div><label>Shut down server</label><small>Stops DarkServe. This is what you use when it runs in the background — start it again from the launcher.</small></div>' +
        '<button class="btn btn-danger" id="btn-shutdown">SHUT DOWN</button></div>'
      : '') +
  '</div>';
  html += '</div>';

  html += '<div id="playlist-panel" class="settings-full"></div>';
  if (admin) html += '<div class="btn-row sticky"><button class="btn btn-primary" id="btn-save">SAVE SETTINGS</button></div>';
  html += '</div>';
  c.innerHTML = html;
  if (admin) {
    document.getElementById('btn-save').addEventListener('click', saveSettings);
    document.getElementById('btn-art').addEventListener('click', startArtwork);
    document.getElementById('btn-shutdown').addEventListener('click', shutdownServer);
    loadArtStatus();
  }
  loadPlaylistPanel(admin);
  loadYtdlpStatus();
  loadCastHint();
}

// shutdownServer stops DarkServe from the browser. It is the way out for the
// AppImage, where the server runs in the background and there is no terminal
// to stop it from.
async function shutdownServer() {
  if (!confirm('Shut down DarkServe?\n\nThe page will go offline until you start it again from the launcher.')) return;
  try {
    await fetch(API + '/api/shutdown', { method: 'POST' });
  } catch (e) {
    // The server closes the connection while it stops — that is expected.
  }
  document.body.innerHTML = '<div class="screen active"><div class="auth-wrap"><div class="login-box glass">' +
    '<h1 class="brand">DARK<span>SERVE</span></h1>' +
    '<p class="tagline">// SERVER STOPPED //</p>' +
    '<p class="muted">Start it again from the app launcher.</p>' +
    '</div></div></div>';
}

async function loadPlaylistPanel(admin) {
  const box = document.getElementById('playlist-panel');
  if (!box) return;
  const lists = (await fetchJSON('/api/iptv/lists')) || [];
  let h = '<div class="sect"><h4>Playlists · ' + lists.length + '</h4>';
  if (lists.length) {
    h += '<table class="users-table"><thead><tr><th>Name</th><th>Source</th><th>Channels</th><th>Last refreshed</th><th></th></tr></thead><tbody>';
    for (const l of lists) {
      h += '<tr><td>' + esc(l.name) + '</td>' +
        '<td><span class="role-pill user">' + esc((l.type || 'url').toUpperCase()) + '</span></td>' +
        '<td>' + l.channels + '</td>' +
        '<td class="muted">' + esc(fmtAgo(l.updated)) + '</td>' +
        '<td class="right">' + (admin
          ? '<button class="btn btn-small" data-act="iptv-refresh" data-id="' + l.id + '">REFRESH</button> ' +
            '<button class="btn btn-danger btn-small" data-act="iptv-del" data-id="' + l.id + '">DELETE</button>'
          : '') + '</td></tr>';
    }
    h += '</tbody></table>';
  } else {
    h += '<p class="hint-line">No playlists yet.</p>';
  }
  if (admin) {
    h += '<div class="form-block"><h5>Add playlist</h5><div class="form-grid">' +
      '<input type="text" id="iptv-name" placeholder="Name (e.g. My Provider)">' +
      '<input type="text" id="iptv-url" placeholder="https://provider.example/get.php?…">' +
      '<button class="btn btn-primary" id="btn-iptv-add">ADD &amp; LOAD</button></div>' +
      '<p class="hint-line">M3U / M3U8 by URL. Channels, groups and logos are read from the playlist.</p></div>';
  }
  h += '</div>';
  box.innerHTML = h;
  if (admin) document.getElementById('btn-iptv-add').addEventListener('click', addIPTVList);
}

function fmtAgo(iso) {
  const t = Date.parse(iso || '');
  if (!t) return 'never';
  const mins = Math.floor((Date.now() - t) / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return mins + ' min ago';
  const hrs = Math.floor(mins / 60);
  if (hrs < 48) return hrs + ' h ago';
  return Math.floor(hrs / 24) + ' days ago';
}

// Playlist changes happen in Settings or on the Live TV page; reload whichever
// one the user is looking at.
function afterPlaylistChange() {
  loadPage(currentPage === 'settings' ? 'settings' : 'iptv');
}

async function loadArtStatus() {
  const el = document.getElementById('art-status');
  if (!el) return;
  try {
    const st = await fetchJSON('/api/artwork');
    const total = st.with_art + st.missing + st.unmatched;
    let line = st.with_art + ' of ' + total + ' items have artwork · ' + st.unmatched + ' not found online';
    if (st.running) line = 'Looking up posters… ' + st.checked + ' checked, ' + st.matched + ' matched';
    else if (st.error) line += ' · last error: ' + st.error;
    if (!st.enabled) line += ' · online lookup is off';
    el.textContent = line;
    el.classList.toggle('busy', !!st.running);
    if (st.running) setTimeout(loadArtStatus, 2000);
  } catch (err) {
    el.textContent = err.message;
  }
}

async function startArtwork() {
  const res = await fetch(API + '/api/artwork', { method: 'POST' });
  if (res.ok) {
    toast('POSTER LOOKUP STARTED');
    loadArtStatus();
  } else {
    toast(await errorText(res, 'LOOKUP FAILED'), true);
  }
}

async function saveSettings() {
  const minSize = parseInt(document.getElementById('set-minsize').value, 10);
  const timeout = parseInt(document.getElementById('set-iptv-timeout').value, 10);
  const maxCh = parseInt(document.getElementById('set-iptv-max').value, 10);
  if (!(minSize >= 1)) { toast('MIN SIZE MUST BE 1 MB OR MORE', true); return; }
  if (!(timeout >= 5 && timeout <= 300)) { toast('TIMEOUT MUST BE 5–300 SECONDS', true); return; }
  if (!(maxCh >= 0)) { toast('MAX CHANNELS CANNOT BE NEGATIVE', true); return; }
  const refreshH = parseInt(document.getElementById('set-iptv-refresh').value || '0', 10);
  const rescanM = parseInt(document.getElementById('set-rescan').value || '0', 10);
  if (!(refreshH >= 0 && refreshH <= 168)) { toast('PLAYLIST REFRESH MUST BE 0–168 HOURS', true); return; }
  if (!(rescanM >= 0 && rescanM <= 1440)) { toast('RESCAN MUST BE 0–1440 MINUTES', true); return; }
  const conc = parseInt(document.getElementById('set-ytdlp-conc').value || '2', 10);
  if (!(conc >= 1 && conc <= 5)) { toast('PARALLEL DOWNLOADS MUST BE 1–5', true); return; }
  const subLang = document.getElementById('set-subs-lang').value.trim();
  if (!/^[a-z]{2,3}$/i.test(subLang)) { toast('SUBTITLE LANGUAGE MUST BE A 2–3 LETTER CODE', true); return; }

  const body = {
    usb_autodetect: document.getElementById('set-autodetect').checked ? '1' : '0',
    usb_paths: document.getElementById('set-usblib').value.trim(),
    local_paths: document.getElementById('set-locals').value.trim(),
    tv_grouping: document.getElementById('set-tvgroup').checked ? '1' : '0',
    min_size_mb: String(minSize),
    media_exts: document.getElementById('set-exts').value.trim(),
    iptv_user_agent: document.getElementById('set-iptv-ua').value.trim(),
    iptv_timeout_sec: String(timeout),
    iptv_max_channels: String(maxCh),
    iptv_refresh_hours: String(refreshH),
    rescan_minutes: String(rescanM),
    ytdlp_path: document.getElementById('set-ytdlp').value.trim(),
    ytdlp_concurrency: document.getElementById('set-ytdlp-conc').value,
    compat_preset: document.getElementById('set-compat-preset').value,
    compat_max_height: document.getElementById('set-compat-height').value,
    subs_search_enabled: document.getElementById('set-subs-on').checked ? '1' : '0',
    subs_key: document.getElementById('set-subs-key').value.trim(),
    subs_lang: document.getElementById('set-subs-lang').value.trim(),
    scan_excludes: document.getElementById('set-excludes').value.trim(),
    cast_enabled: document.getElementById('set-cast').checked ? '1' : '0',
    cast_url: document.getElementById('set-cast-url').value.trim(),
    artwork_online: document.getElementById('set-art-online').checked ? '1' : '0',
    artwork_tmdb_key: document.getElementById('set-tmdb').value.trim()
  };
  const res = await postRaw('/api/settings', body);
  if (res.ok) {
    toast('SETTINGS SAVED · APPLIED LIVE');
    loadPage('settings');
  } else {
    toast(await errorText(res, 'SAVE FAILED'), true);
  }
}

/* ------------ users page ------------ */
async function pageUsers(c) {
  const users = await fetchJSON('/api/users');
  let html = '<div class="panel wide">';
  html += '<div class="sect"><h4>Add user</h4>' +
    '<div class="form-grid">' +
      '<input type="text" id="nu-name" placeholder="Username" autocomplete="off">' +
      '<input type="password" id="nu-pass" placeholder="Password" autocomplete="new-password">' +
      '<button class="btn btn-primary" id="btn-adduser">CREATE USER</button>' +
    '</div>' +
    '<p class="hint-line">Users can browse and play. Only admins can change settings or manage accounts.</p>' +
  '</div>';
  html += '<div class="sect"><h4>Accounts · ' + users.length + '</h4><table class="users-table"><thead><tr><th>User</th><th>Role</th><th></th></tr></thead><tbody>';
  for (const u of users) {
    const self = u.id === currentUser.id;
    html += '<tr data-user="' + u.id + '"><td>' + esc(u.username) + (self ? ' <span class="you">you</span>' : '') + '</td>' +
      '<td><span class="role-pill ' + esc(u.role) + '">' + esc(u.role.toUpperCase()) + '</span></td>' +
      '<td class="right">' +
        '<button class="btn btn-small" data-act="reset-pass" data-id="' + u.id + '" data-name="' + escAttr(u.username) + '">RESET PASSWORD</button> ' +
        (u.role !== 'admin' && !self
          ? '<button class="btn btn-danger btn-small" data-act="del-user" data-id="' + u.id + '">DELETE</button>' : '') +
      '</td></tr>';
  }
  html += '</tbody></table></div></div>';
  c.innerHTML = html;
  document.getElementById('btn-adduser').addEventListener('click', addUser);
}

// Reset a password without losing the watch progress of that account.
function showResetForm(id, name) {
  const row = document.querySelector('tr[data-user="' + id + '"]');
  if (!row) return;
  row.innerHTML = '<td colspan="3"><div class="form-grid">' +
    '<input type="password" id="np-' + id + '" placeholder="New password for ' + esc(name) + '" autocomplete="new-password">' +
    '<input type="password" id="np2-' + id + '" placeholder="Repeat the password" autocomplete="new-password">' +
    '<button class="btn btn-primary btn-small" data-act="save-pass" data-id="' + id + '">SAVE</button>' +
    '<button class="btn btn-ghost btn-small" data-act="cancel-pass" data-id="' + id + '">CANCEL</button>' +
    '</div></td>';
  const first = document.getElementById('np-' + id);
  if (first) first.focus();
}

async function saveNewPassword(id) {
  const p1 = document.getElementById('np-' + id).value;
  const p2 = document.getElementById('np2-' + id).value;
  if (p1 !== p2) { toast('PASSWORDS DO NOT MATCH', true); return; }
  if (p1.length < 8) { toast('PASSWORD MUST BE AT LEAST 8 CHARACTERS', true); return; }
  const res = await postRaw('/api/users/password', { id: Number(id), password: p1 });
  if (res.ok) { toast('PASSWORD CHANGED'); loadPage('users'); }
  else toast(await errorText(res, 'COULD NOT CHANGE THE PASSWORD'), true);
}

async function addUser() {
  const res = await postRaw('/api/users', {
    username: document.getElementById('nu-name').value.trim(),
    password: document.getElementById('nu-pass').value,
    role: 'user'
  });
  if (res.ok) { toast('USER CREATED'); loadPage('users'); }
  else toast(await errorText(res, 'CREATE FAILED'), true);
}

async function delUser(id) {
  if (!confirm('Delete this user and their sessions?')) return;
  const res = await fetch(API + '/api/users?id=' + id, { method: 'DELETE' });
  if (res.ok) { toast('USER REMOVED'); loadPage('users'); }
  else toast(await errorText(res, 'DELETE FAILED'), true);
}

/* ------------ helpers ------------ */
function empty(msg, hint) {
  return '<div class="empty-state"><div class="empty-state-icon">&#9633;</div><p>' + esc(msg) + '</p>' +
    (hint ? '<div class="hint">' + esc(hint) + '</div>' : '') + '</div>';
}

function postRaw(url, body) {
  return fetch(API + url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
}

async function errorText(res, fallback) {
  try { return (await res.json()).error || fallback; } catch (e) { return fallback; }
}

function fetchJSON(url) {
  return fetch(API + url).then(async function (r) {
    const body = await r.json().catch(function () { return null; });
    if (r.status === 401) {
      showLogin();
      const err = new Error('session expired');
      err.authExpired = true;
      throw err;
    }
    if (!r.ok) throw new Error((body && body.error) || ('request failed (' + r.status + ')'));
    return body;
  });
}

function esc(s) {
  if (s === null || s === undefined) return '';
  const d = document.createElement('div');
  d.textContent = String(s);
  return d.innerHTML;
}

function escAttr(s) {
  return esc(s).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

function fmtSize(b) {
  if (!b) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (b >= 1024 && i < units.length - 1) { b /= 1024; i++; }
  return b.toFixed(b >= 100 || i === 0 ? 0 : 1) + ' ' + units[i];
}

function toast(msg, isErr) {
  const t = document.createElement('div');
  t.className = 'toast' + (isErr ? ' err' : '');
  t.textContent = msg;
  document.getElementById('toasts').appendChild(t);
  setTimeout(function () { t.remove(); }, 3000);
}

async function loadCastHint() {
  const el = document.getElementById('cast-hint');
  if (!el) return;
  try {
    const n = await fetchJSON('/api/network');
    el.innerHTML = 'TVs should reach this server at <code>' + esc(n.base_url || 'unknown') + '</code>' +
      (n.addresses && n.addresses.length > 1 ? ' (also ' + esc(n.addresses.slice(1).join(', ')) + ')' : '') +
      '<br>Casting needs the TV on the same network as this machine.';
  } catch (err) { el.textContent = err.message; }
}

async function loadYtdlpStatus() {
  const el = document.getElementById('ytdlp-status');
  if (!el) return;
  try {
    const st = await fetchJSON('/api/ytdlp');
    if (st.available) {
      el.innerHTML = '&#10003; yt-dlp found' + (st.version ? ' (' + esc(st.version) + ')' : '') + (st.configured_path ? ' at ' + esc(st.configured_path) : '');
    } else {
      el.innerHTML = '&#10007; yt-dlp not found — the Downloads page needs it';
    }
  } catch (err) { el.textContent = err.message; }
}

/* ---------- casting to a TV (Chromecast) ---------- */
function loadCastSdk() {
  if (document.getElementById('cast-sdk')) return;
  window.__onGCastApiAvailable = function (available) {
    if (!available || !window.cast) return;
    try {
      castCtx = window.cast.framework.CastContext.getInstance();
      castCtx.setOptions({
        receiverApplicationId: CAST_APP_ID,
        autoJoinPolicy: 'page_scoped'
      });
      castReady = true;
      castCtx.addEventListener(
        window.cast.framework.CastContextEventType.SESSION_STATE_CHANGED, onCastState);
      updateCastUi();
    } catch (e) { /* SDK loaded but blocked by the browser */ }
  };
  const sc = document.createElement('script');
  sc.id = 'cast-sdk';
  sc.src = 'https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1';
  document.head.appendChild(sc);
}

function onCastState() {
  castSession = castCtx ? castCtx.getCurrentSession() : null;
  updateCastUi();
  if (castSession && castMedia) loadCastMedia(castMedia);
}

function updateCastUi() {
  const btn = document.getElementById('player-cast');
  if (!btn) return;
  const on = !!castSession;
  btn.classList.toggle('casting', on);
  btn.innerHTML = on ? '&#9679; CASTING' : '&#9654; CAST';
  btn.title = on
    ? 'Casting to ' + (castSession.getCastDevice ? castSession.getCastDevice().friendlyName : 'a device')
    : 'Cast to a TV';
  const casting = document.getElementById('player-casting');
  if (casting) {
    casting.classList.toggle('hidden', !on);
    if (on) {
      const name = castSession.getCastDevice ? castSession.getCastDevice().friendlyName : 'your TV';
      casting.querySelector('.casting-name').textContent = name;
    }
  }
  // Local playback yields to the TV.
  const v = document.getElementById('player');
  if (on && !v.paused) v.pause();
}

async function startCast() {
  if (!castEnabled) { toast('ENABLE CASTING IN SETTINGS FIRST', true); return; }
  // A TV has no login cookie, so the media URL carries a signed token instead.
  if (castLibId && !castToken) {
    try {
      const t = await fetchJSON('/api/cast-token/' + castLibId);
      if (t && t.token) castToken = t.token;
    } catch (e) { /* fall back to the plain URL */ }
  }
  if (!castCtx) { loadCastSdk(); toast('STARTING CAST SERVICE…'); return; }
  if (castSession) { // already connected: cast what is loaded now
    loadCastMedia(castMedia || buildCastMedia());
    return;
  }
  try {
    await castCtx.requestSession();
  } catch (e) {
    return; // the user closed the picker
  }
  castSession = castCtx.getCurrentSession();
  updateCastUi();
  loadCastMedia(castMedia || buildCastMedia());
}

// buildCastMedia assembles what the TV should play from the current item.
function buildCastMedia() {
  const v = document.getElementById('player');
  const src = v.currentSrc || v.src || '';
  let path = '';
  if (src.indexOf('/api/') !== -1) {
    path = src.slice(src.indexOf('/api/'));
  }
  let contentType = 'video/mp4';
  if (window._hls || src.indexOf('.m3u8') !== -1) contentType = 'application/x-mpegurl';
  else if (v.classList.contains('audio-mode')) contentType = 'audio/mpeg';
  return { path: path, contentType: contentType, title: currentTitle, id: currentMediaId, position: castStartAt || v.currentTime || 0 };
}

function castUrl(path) {
  if (/^https?:\/\//i.test(path)) return path;
  const base = (castBase || window.location.origin).replace(/\/$/, '');
  if (!castToken) return base + path;
  return base + path + (path.indexOf('?') === -1 ? '?token=' : '&token=') + encodeURIComponent(castToken);
}

async function loadCastMedia(m) {
  if (!castSession || !m) return;
  if (!m.path) { toast('NOTHING LOADED TO CAST', true); return; }
  castMedia = m;
  const chrome = window.chrome || {};
  const media = new chrome.cast.media.MediaInfo(castUrl(m.path), m.contentType);
  media.metadata = new chrome.cast.media.GenericMediaMetadata();
  media.metadata.metadataType = chrome.cast.media.MetadataType.GENERIC;
  media.metadata.title = m.title || 'Now playing';
  media.streamType = chrome.cast.media.StreamType.BUFFERED;
  if (m.contentType === 'application/x-mpegurl') {
    media.streamType = chrome.cast.media.StreamType.LIVE;
  }
  media.customData = {};

  // Poster, resolvable from the TV side too.
  if (castPoster) media.metadata.images = [{ url: castUrl(castPoster) }];

  // Sidecar subtitles travel with the stream.
  media.tracks = [];
  (castSubs || []).forEach(function (s, i) {
    const t = new chrome.cast.media.Track(i, chrome.cast.media.TrackType.TEXT);
    t.trackContentId = castUrl(s.src);
    t.trackContentType = 'text/vtt';
    // TrackType / TextTrackType are members of chrome.cast.media.
    t.subtype = chrome.cast.media.TextTrackType.SUBTITLES;
    t.name = s.label;
    t.language = 'en';
    t.customData = null;
    media.tracks.push(t);
  });

  const request = new chrome.cast.media.LoadRequest(media);
  request.autoplay = true;
  request.currentTime = m.position || 0;
  try {
    await castSession.loadMedia(request);
    toast('CASTING TO ' + (castSession.getCastDevice ? castSession.getCastDevice().friendlyName : 'TV'));
  } catch (e) {
    toast('CAST FAILED — IS THE SERVER REACHABLE FROM THE TV?', true);
  }
}

function stopCast() {
  if (!castSession) return;
  try { castSession.endSession(true); } catch (e) {}
  castSession = null;
  castMedia = null;
  updateCastUi();
  toast('STOPPED CASTING');
}
