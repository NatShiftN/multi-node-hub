<script setup lang="ts">
import { RouterView } from 'vue-router'
import { useUIMode } from './uiMode'

// Keep mode initialization at the app shell so every route observes the same
// localStorage-backed state, even when it is the first page loaded.
const { uiMode } = useUIMode()
</script>

<template>
  <div :data-ui-mode="uiMode" class="min-h-screen transition-colors duration-300">
    <RouterView />
  </div>
</template>
