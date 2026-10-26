<script setup lang="ts">
import { locale, toggleLocale } from '../i18n'

defineProps<{ compact?: boolean }>()
</script>

<template>
  <button
    type="button"
    class="focus-ring inline-flex items-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-1 font-mono text-[10px] text-slate-400 transition hover:text-slate-200"
    :aria-label="locale === 'en' ? 'Switch to Chinese' : 'Switch to English'"
    @click="toggleLocale"
  >
    <svg viewBox="0 0 24 24" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.5 2.5 15 0 18M12 3c-2.5 2.5-2.5 15 0 18" />
    </svg>
    {{ locale === 'en' ? 'EN' : '中文' }}
  </button>
</template>
