const PAGE_IDS = {
  AUTH: 'auth',
  LOBBY: 'lobby',
  MATCHING: 'matching',
  MATCH_FOUND: 'match-found',
  BATTLE_LOADING: 'battle-loading',
  BATTLE: 'battle',
  RESULT: 'result',
  RECONNECTING: 'reconnecting',
  MATCH_CANCELING: 'matching',
  LEAVING_BATTLE: 'battle',
};

export function createViewManager() {
  const sections = Array.from(document.querySelectorAll('[data-page]'));

  function show(page) {
    const active = PAGE_IDS[page];
    document.body.dataset.page = page.toLowerCase();
    sections.forEach((section) => {
      section.classList.toggle('hidden', section.id !== active);
    });
  }

  function setText(id, value) {
    const node = document.getElementById(id);
    if (node) {
      node.textContent = value ?? '';
    }
  }

  function setHTML(id, value) {
    const node = document.getElementById(id);
    if (node) {
      node.innerHTML = value ?? '';
    }
  }

  function setDisabled(selector, disabled) {
    document.querySelectorAll(selector).forEach((node) => {
      node.disabled = disabled;
    });
  }

  return { show, setText, setHTML, setDisabled };
}
