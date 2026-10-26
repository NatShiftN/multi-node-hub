<script setup lang="ts">
import { useUIMode } from '../uiMode'

const { isSimple, setUIMode } = useUIMode()
</script>

<template>
  <div
    class="ui-mode-toggle inline-flex items-center rounded-full border border-white/[0.08] bg-black/25 p-0.5"
    role="group"
    aria-label="介面模式"
    title="極簡模式隱藏開發者與高危操作；商業模式釋放完整功能"
  >
    <button
      type="button"
      class="focus-ring rounded-full px-2.5 py-1.5 font-mono text-[9px] tracking-[0.08em] transition-all duration-300"
      :class="isSimple ? 'bg-emerald-400/15 text-emerald-200 shadow-[0_0_18px_rgba(52,211,153,.08)]' : 'text-slate-500 hover:text-slate-300'"
      :aria-pressed="isSimple"
      @click="setUIMode('simple')"
    >
      極簡自用
    </button>
    <button
      type="button"
      class="focus-ring rounded-full px-2.5 py-1.5 font-mono text-[9px] tracking-[0.08em] transition-all duration-300"
      :class="!isSimple ? 'bg-violet-400/15 text-violet-200 shadow-[0_0_18px_rgba(167,139,250,.08)]' : 'text-slate-500 hover:text-slate-300'"
      :aria-pressed="!isSimple"
      @click="setUIMode('pro')"
    >
      商業營運
    </button>
  </div>
</template>
