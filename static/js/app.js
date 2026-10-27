// ============================================================
// AutoSail — 前端 JS 模块
// ============================================================

// -------------------- 首页链接刷新 --------------------
(function () {
  const homeLink = document.getElementById('siteHomeLink');
  if (!homeLink) return;
  homeLink.addEventListener('click', function (event) {
    const isHomePath = window.location.pathname === '/';
    const noQuery = !window.location.search;
    if (isHomePath && noQuery) {
      event.preventDefault();
      window.location.reload();
    }
  });
})();

// -------------------- 快捷键拦截 + 自定义右键菜单 --------------------
(function () {
  function shouldBlockShortcut(event) {
    const key = (event.key || '').toLowerCase();
    const ctrlOrMeta = event.ctrlKey || event.metaKey;
    if (event.key === 'F12') return true;
    if (ctrlOrMeta && event.shiftKey && (key === 'i' || key === 'j' || key === 'c')) return true;
    if (ctrlOrMeta && (key === 's' || key === 'u')) return true;
    return false;
  }

  document.addEventListener('keydown', function (event) {
    if (shouldBlockShortcut(event)) {
      event.preventDefault();
      event.stopPropagation();
    }
  }, true);

  const menu = document.createElement('div');
  menu.className = 'custom-context-menu';
  menu.innerHTML = '<button type="button"><span style="font-size:14px;opacity:0.7;line-height:1">↻</span><span>刷新页面</span></button>';
  document.body.appendChild(menu);

  const refreshBtn = menu.querySelector('button');
  refreshBtn.addEventListener('click', function () {
    menu.classList.remove('ctx-visible');
    window.location.reload();
  });

  function hideMenu() { menu.classList.remove('ctx-visible'); }

  document.addEventListener('contextmenu', function (event) {
    event.preventDefault();
    menu.style.cssText = 'display:block;left:-9999px;top:-9999px';
    const w = menu.offsetWidth;
    const h = menu.offsetHeight;
    let x = event.clientX;
    let y = event.clientY;
    if (x + w > window.innerWidth)  x = Math.max(8, window.innerWidth  - w - 8);
    if (y + h > window.innerHeight) y = Math.max(8, window.innerHeight - h - 8);
    menu.style.cssText = '';
    menu.style.left = x + 'px';
    menu.style.top  = y + 'px';
    menu.classList.remove('ctx-visible');
    void menu.offsetWidth;
    menu.classList.add('ctx-visible');
  }, true);

  document.addEventListener('click', hideMenu, true);
  document.addEventListener('scroll', hideMenu, true);
  window.addEventListener('resize', hideMenu);
  window.addEventListener('blur', hideMenu);
})();

// -------------------- 代理出口 IP 检测 --------------------
(function(){
  const btn = document.getElementById('proxyCheckBtn');
  const result = document.getElementById('proxyCheckResult');
  if(!btn || !result) return;

  btn.addEventListener('click', async () => {
    const proxyInput = document.querySelector('input[name="proxy"]');
    const proxy = proxyInput ? proxyInput.value.trim() : '';
    if(!proxy){
      result.classList.remove('hidden');
      result.classList.remove('bg-emerald-50','border-emerald-200', 'text-emerald-800');
      result.classList.add('bg-amber-50','border-amber-200','text-amber-800', 'border');
      result.textContent = '请先填写 Proxy 再检测。';
      return;
    }
    result.classList.remove('hidden');
    result.className = 'text-xs bg-gray-50 border border-gray-200 rounded-xl p-3 text-slate-600 animate-pulse';
    result.textContent = '检测中...';
    try{
      const r = await fetch('/proxy/check?proxy='+encodeURIComponent(proxy));
      const j = await r.json();
      result.classList.remove('animate-pulse');
      if(j && j.ok){
        result.className = 'text-xs bg-emerald-50 border border-emerald-200 rounded-xl p-3 text-emerald-800';
        const asText = (j.as||'').trim();
        result.innerHTML = '<div class="flex items-center gap-2"><svg class="w-4 h-4 text-emerald-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg><span class="font-bold">Connected</span></div>' +
                          '<div class="mt-1 pl-6">IP: <span class="font-mono text-emerald-700">'+(j.ip||'')+'</span></div>' +
                          '<div class="pl-6 mt-0.5 opacity-80">AS: <span class="font-mono">'+(asText||'N/A')+'</span></div>';
      }else{
        result.className = 'text-xs bg-rose-50 border border-rose-200 rounded-xl p-3 text-rose-800';
        result.textContent = (j && j.error) ? j.error : '检测失败';
      }
    }catch(e){
      result.className = 'text-xs bg-rose-50 border border-rose-200 rounded-xl p-3 text-rose-800';
      result.textContent = '检测失败：' + (e && e.message ? e.message : e);
    }
  });
})();

// -------------------- 密钥列表搜索过滤 --------------------
(function(){
  const searchInput = document.getElementById('accountSearch');
  const list = document.getElementById('accountList');
  const empty = document.getElementById('accountEmpty');
  if(!searchInput || !list || !empty) return;

  function normalize(text){ return (text || '').toLowerCase(); }

  function update(){
    const term = normalize(searchInput.value);
    let visible = 0;
    list.querySelectorAll('.account-row').forEach((row) => {
      const nameNode = row.querySelector('[data-name]');
      const name = normalize(nameNode ? nameNode.getAttribute('data-name') : '');
      const matched = !term || name.includes(term);
      row.classList.toggle('hidden', !matched);
      if(matched) visible += 1;
    });
    empty.classList.toggle('hidden', visible > 0);
  }

  searchInput.addEventListener('input', update);
  update();
})();

// -------------------- 账号列表拖拽排序 --------------------
(function(){
  const list = document.getElementById('accountList');
  if(!list) return;
  const rows = Array.from(list.querySelectorAll('.account-row'));
  if(rows.length < 2) return;

  let dragRow = null;

  // 仅在按住手柄时启用行拖拽，避免影响行内按钮点击
  rows.forEach((row) => {
    const handle = row.querySelector('.drag-handle');
    if(!handle) return;
    handle.addEventListener('mousedown', () => { row.draggable = true; });
    handle.addEventListener('touchstart', () => { row.draggable = true; }, {passive: true});
  });

  function clearDragStyles(){
    rows.forEach((row) => {
      row.classList.remove('opacity-40', 'ring-2', 'ring-indigo-300', 'rounded-md');
      row.style.transform = '';
    });
  }

  list.addEventListener('dragstart', (event) => {
    const row = event.target.closest('.account-row');
    if(!row || !row.draggable){ event.preventDefault(); return; }
    dragRow = row;
    row.classList.add('opacity-40');
    event.dataTransfer.effectAllowed = 'move';
    try{ event.dataTransfer.setData('text/plain', row.dataset.keyId || ''); }catch(e){}
  });

  list.addEventListener('dragover', (event) => {
    if(!dragRow) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    const target = event.target.closest('.account-row');
    if(!target || target === dragRow || !list.contains(target)) return;
    const rect = target.getBoundingClientRect();
    const before = (event.clientY - rect.top) < rect.height / 2;
    if(before) list.insertBefore(dragRow, target);
    else list.insertBefore(dragRow, target.nextSibling);
  });

  list.addEventListener('drop', (event) => { if(dragRow) event.preventDefault(); });

  list.addEventListener('dragend', async () => {
    if(!dragRow) return;
    const row = dragRow;
    dragRow = null;
    row.draggable = false;
    clearDragStyles();

    const ids = Array.from(list.querySelectorAll('.account-row'))
      .map((r) => r.dataset.keyId)
      .filter(Boolean);
    if(ids.length < 2) return;

    const body = new URLSearchParams();
    body.append('csrf_token', list.dataset.csrf || '');
    ids.forEach((id) => body.append('order', id));
    try{
      const response = await fetch('/auth/reorder', {
        method: 'POST',
        body: body,
        headers: {'X-Requested-With': 'ajax-form'}
      });
      if(!response.ok) window.location.reload();
    }catch(e){
      window.location.reload();
    }
  });
})();

// -------------------- 实例列表静默刷新 --------------------
// 存在过渡态实例时静默刷新，间隔由服务端按缓存时长注入（见 main.go instPollIntervalMS）
(function(){
  const TRANSITIONAL = ['pending', 'stopping', 'shutting-down'];
  const POLL_MS = Number(document.body.dataset.instPollMs) || 12000;
  let inFlight = false;
  const MAX_POLLS = Math.ceil(60000 / POLL_MS); // 单轮过渡态最多自动刷新约 1 分钟
  let pollCount = 0;

  // 用户手动操作（刷新/重启等）后重新给一轮预算
  window.__instPollReset = function(){ pollCount = 0; };

  function hasTransitional(){
    return Array.from(document.querySelectorAll('[data-state]')).some((el) =>
      TRANSITIONAL.includes((el.getAttribute('data-state') || '').toLowerCase())
    );
  }

  // 副本上剥掉动画类再比较，替换用原件
  function normalizedHtml(root){
    const clone = root.cloneNode(true);
    clone.classList.remove('animate-fade-in');
    return clone.outerHTML;
  }

  async function silentRefresh(){
    if(inFlight) return;
    inFlight = true;
    try{
      const resp = await fetch(window.location.href, {headers: {'X-Requested-With': 'fetch-tab'}});
      if(!resp.ok) return;
      const html = await resp.text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const newRoot = doc.getElementById('tab-root');
      const curRoot = document.getElementById('tab-root');
      if(!newRoot || !curRoot) return;
      // 替换前重校验，期间有变动则放弃本次
      if(document.getElementById('tab-root') !== curRoot) return;
      if(window.__ajaxFormBusy) return;
      if(document.querySelector('.custom-confirm-mask.is-open')) return;
      if(normalizedHtml(newRoot) === normalizedHtml(curRoot)) return;
      newRoot.classList.remove('animate-fade-in');
      curRoot.replaceWith(newRoot);
    }catch(e){ /* 网络抖动等下次轮询 */ }
    finally{ inFlight = false; }
  }

  setInterval(() => {
    if(document.visibilityState !== 'visible') return;          // 后台标签页不请求
    if(document.querySelector('.custom-confirm-mask.is-open')) return; // 确认弹窗打开时不刷
    if(!document.getElementById('tab-root')) return;
    if(!hasTransitional()){ pollCount = 0; return; }            // 回到稳定态即重置预算
    if(pollCount >= MAX_POLLS) return;                          // 超预算不再请求
    pollCount++;
    silentRefresh();
  }, POLL_MS);
})();

// -------------------- 复制 IP + 标签页 AJAX 切换 --------------------
async function copyText(text){
  if(!text) return;
  try{ await navigator.clipboard.writeText(text); }
  catch(e){
    const t=document.createElement('textarea');
    t.value=text;document.body.appendChild(t);t.select();
    document.execCommand('copy');document.body.removeChild(t);
  }
}

(function(){
  const tabRootId = 'tab-root';

  async function replaceTabContent(url, pushState){
    try{
      const response = await fetch(url, {headers: {'X-Requested-With': 'fetch-tab'}});
      if(!response.ok){ if(pushState) window.location.href = url; return; }
      const html = await response.text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const newRoot = doc.getElementById(tabRootId);
      const currentRoot = document.getElementById(tabRootId);
      if(!newRoot || !currentRoot){ if(pushState) window.location.href = url; return; }
      currentRoot.replaceWith(newRoot);
      if(pushState){
        window.history.pushState({tab: true}, '', url);
        if(window.__cleanURLMsg) window.__cleanURLMsg();
      }
    }catch(e){ if(pushState) window.location.href = url; }
  }

  document.addEventListener('click', (event) => {
    const link = event.target.closest('a[data-tab-link]');
    if(!link) return;
    const url = link.getAttribute('href');
    if(!url || url.startsWith('javascript:')) return;
    if(link.target && link.target !== '_self') return;
    event.preventDefault();
    replaceTabContent(url, true);
  });

  document.addEventListener('change', (event) => {
    const select = event.target.closest('select[data-tab-select]');
    if(!select) return;
    const prefix = select.dataset.tabUrlPrefix || '';
    if(!prefix) return;
    const url = prefix + encodeURIComponent(select.value || '');
    replaceTabContent(url, true);
  });

  window.addEventListener('popstate', () => { replaceTabContent(window.location.href, false); });
})();

// -------------------- 确认弹窗 + AJAX 表单提交 --------------------
(function(){
  const confirmMask = document.createElement('div');
  confirmMask.className = 'custom-confirm-mask';
  confirmMask.innerHTML = '' +
    '<div class="custom-confirm-dialog" data-variant="danger" role="dialog" aria-modal="true" aria-labelledby="customConfirmTitle">' +
      '<div class="custom-confirm-header">' +
        '<span class="custom-confirm-icon">!</span>' +
        '<span id="customConfirmTitle" class="custom-confirm-title">危险操作确认</span>' +
      '</div>' +
      '<p class="custom-confirm-message"></p>' +
      '<div class="custom-confirm-actions">' +
        '<button type="button" class="custom-confirm-btn cancel">取消</button>' +
        '<button type="button" class="custom-confirm-btn confirm">确认</button>' +
      '</div>' +
    '</div>';
  document.body.appendChild(confirmMask);

  const confirmMessage = confirmMask.querySelector('.custom-confirm-message');
  const confirmCancelBtn = confirmMask.querySelector('.custom-confirm-btn.cancel');
  const confirmOkBtn = confirmMask.querySelector('.custom-confirm-btn.confirm');
  const confirmDialog = confirmMask.querySelector('.custom-confirm-dialog');
  const confirmTitle = confirmMask.querySelector('.custom-confirm-title');
  const confirmIcon = confirmMask.querySelector('.custom-confirm-icon');
  let confirmResolver = null;
  let lastFocusedElement = null;

  function resolveVariant(variant, actionLabel, message) {
    if (variant === 'danger' || variant === 'default') return variant;
    const merged = (String(actionLabel || '') + ' ' + String(message || '')).toLowerCase();
    if (/(删除|终止|清空|移除|销毁|停用|drop|terminate|delete|remove)/.test(merged)) return 'danger';
    return 'default';
  }

  function closeConfirm(result) {
    confirmMask.classList.remove('is-open');
    document.body.style.removeProperty('overflow');
    if (confirmResolver) {
      const resolve = confirmResolver;
      confirmResolver = null;
      resolve(result);
    }
    if (lastFocusedElement && typeof lastFocusedElement.focus === 'function') lastFocusedElement.focus();
  }

  function showConfirm(message, actionLabel, variant) {
    if (confirmResolver) closeConfirm(false);
    lastFocusedElement = document.activeElement;
    const finalVariant = resolveVariant(variant, actionLabel, message);
    confirmDialog.setAttribute('data-variant', finalVariant);
    if (finalVariant === 'danger') {
      confirmTitle.textContent = '危险操作确认';
      confirmIcon.textContent = '!';
    } else {
      confirmTitle.textContent = '操作确认';
      confirmIcon.textContent = 'i';
    }
    confirmMessage.textContent = message || '确定继续吗？';
    confirmOkBtn.textContent = '确定';
    confirmMask.classList.add('is-open');
    document.body.style.overflow = 'hidden';
    setTimeout(function () { confirmCancelBtn.focus(); }, 0);
    return new Promise(function (resolve) { confirmResolver = resolve; });
  }

  confirmCancelBtn.addEventListener('click', function () { closeConfirm(false); });
  confirmOkBtn.addEventListener('click', function () { closeConfirm(true); });
  confirmMask.addEventListener('click', function (event) {
    if (event.target === confirmMask) closeConfirm(false);
  });
  document.addEventListener('keydown', function (event) {
    if (!confirmMask.classList.contains('is-open')) return;
    if (event.key === 'Escape') { event.preventDefault(); closeConfirm(false); }
    if (event.key === 'Tab' && confirmDialog) {
      const focusables = [confirmCancelBtn, confirmOkBtn].filter(Boolean);
      if (!focusables.length) return;
      const current = document.activeElement;
      const index = focusables.indexOf(current);
      event.preventDefault();
      const nextIndex = event.shiftKey
        ? (index <= 0 ? focusables.length - 1 : index - 1)
        : (index === focusables.length - 1 ? 0 : index + 1);
      focusables[nextIndex].focus();
    }
  });

  async function handleAjaxSubmit(form){
    // AJAX 提交计数，静默刷新据此避让
    window.__ajaxFormBusy = (window.__ajaxFormBusy || 0) + 1;
    const submitButtons = Array.from(form.querySelectorAll('button')).filter((btn) => {
      const type = (btn.getAttribute('type') || 'submit').toLowerCase();
      return type === 'submit';
    });
    submitButtons.forEach((btn) => {
      btn.disabled = true;
      btn.dataset.originalText = btn.textContent;
      btn.textContent = '处理中...';
    });
    try{
      const response = await fetch(form.action, {
        method: (form.method || 'POST').toUpperCase(),
        body: new FormData(form),
        headers: {'X-Requested-With': 'ajax-form'}
      });
      if(!response.ok){ window.location.href = form.action; return; }
      const html = await response.text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const newTabRoot = doc.getElementById('tab-root');
      const currentTabRoot = document.getElementById('tab-root');
      if(newTabRoot && currentTabRoot) currentTabRoot.replaceWith(newTabRoot);
      if(window.__instPollReset) window.__instPollReset();
      const newFlashRoot = doc.getElementById('flash-root');
      const currentFlashRoot = document.getElementById('flash-root');
      if(newFlashRoot && currentFlashRoot){
        currentFlashRoot.replaceWith(newFlashRoot);
        if(window.__armFlashAutoDismiss) window.__armFlashAutoDismiss(newFlashRoot);
      }
      if(response.url && response.url !== window.location.href) window.history.pushState({form: true}, '', response.url);
      if(window.__cleanURLMsg) window.__cleanURLMsg();
    }catch(e){ window.location.href = form.action; }
    finally{
      window.__ajaxFormBusy = Math.max(0, (window.__ajaxFormBusy || 0) - 1);
      submitButtons.forEach((btn) => {
        btn.disabled = false;
        if(btn.dataset.originalText){
          btn.textContent = btn.dataset.originalText;
          delete btn.dataset.originalText;
        }
      });
    }
  }

  document.addEventListener('submit', (event) => {
    const form = event.target;
    if(!(form instanceof HTMLFormElement)) return;
    if(event.defaultPrevented) return;

    const confirmText = form.getAttribute('data-confirm');
    if(confirmText){
      event.preventDefault();
      const actionLabel = form.getAttribute('data-confirm-action') || '确认';
      const variant = form.getAttribute('data-confirm-variant') || '';
      showConfirm(confirmText, actionLabel, variant).then((ok) => {
        if(!ok) return;
        if(form.matches('[data-ajax]')){ handleAjaxSubmit(form); return; }
        form.submit();
      });
      return;
    }

    if(!form.matches('[data-ajax]')) return;
    event.preventDefault();
    handleAjaxSubmit(form);
  });
})();

/* Flash 提示：2 秒后自动淡出消失 */
(function () {
  var FLASH_TTL = 2000;
  var FLASH_FADE = 400;
  var timer = null;

  function armFlashAutoDismiss(root) {
    if (!root) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(function () {
      // 先清除进入动画（animation-fill-mode: forwards 会覆盖内联样式导致淡出不生效），再纯透明度淡出，避免消失时抖动
      root.style.animation = 'none';
      root.style.transition = 'opacity ' + FLASH_FADE + 'ms ease';
      root.style.opacity = '0';
      setTimeout(function () {
        if (root && root.parentNode) root.remove();
      }, FLASH_FADE);
    }, FLASH_TTL);
  }

  function cleanURLMsg() {
    var url = new URL(window.location.href);
    if (url.searchParams.has('msg')) {
      url.searchParams.delete('msg');
      window.history.replaceState({}, '', url.pathname + url.search + url.hash);
    }
  }

  // 页面初始加载时若有 flash-root，自动倒计时消失；同时清除 URL 中的 msg 参数，避免刷新后提示重现
  var initialFlash = document.getElementById('flash-root');
  if (initialFlash) armFlashAutoDismiss(initialFlash);
  cleanURLMsg();

  // 暴露给 AJAX 更新 flash-root 后重新计时、以及 URL 更新后清除 msg
  window.__armFlashAutoDismiss = armFlashAutoDismiss;
  window.__cleanURLMsg = cleanURLMsg;
})();
