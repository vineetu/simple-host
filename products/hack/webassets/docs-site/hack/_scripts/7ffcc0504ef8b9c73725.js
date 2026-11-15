
	(() => {
		const openBtn = document.querySelector('button[data-open-modal]');
		const shortcut = openBtn?.querySelector('kbd');
		if (!openBtn || !(shortcut instanceof HTMLElement)) return;
		const platformKey = shortcut.querySelector('kbd');
		if (platformKey && /(Mac|iPhone|iPod|iPad)/i.test(navigator.platform)) {
			platformKey.textContent = '⌘';
			openBtn.setAttribute('aria-keyshortcuts', 'Meta+K');
		}
		shortcut.style.display = '';
	})();
