import { computed, readonly, ref, watch } from 'vue'

export type UIMode = 'simple' | 'pro'

const STORAGE_KEY = 'nodehub.ui_mode'

function loadMode(): UIMode {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'pro' || stored === 'simple') return stored
  } catch {
    // Private mode / storage disabled: default to the calm experience.
  }
  return 'simple'
}

const mode = ref<UIMode>(loadMode())

function applyDocumentMode(value: UIMode): void {
  if (typeof document !== 'undefined') {
    document.documentElement.dataset.uiMode = value
  }
}

applyDocumentMode(mode.value)

watch(mode, (value) => {
  applyDocumentMode(value)
  try {
    localStorage.setItem(STORAGE_KEY, value)
  } catch {
    // The in-memory mode still works for this tab.
  }
})

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key === STORAGE_KEY && (event.newValue === 'simple' || event.newValue === 'pro')) {
      mode.value = event.newValue
    }
  })
}

export function useUIMode() {
  return {
    uiMode: readonly(mode),
    isSimple: computed(() => mode.value === 'simple'),
    isPro: computed(() => mode.value === 'pro'),
    setUIMode: (value: UIMode) => {
      mode.value = value
    },
    toggleUIMode: () => {
      mode.value = mode.value === 'simple' ? 'pro' : 'simple'
    },
  }
}
