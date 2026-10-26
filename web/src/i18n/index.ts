import { ref } from 'vue'
import en from './en.json'

type Locale = 'zh' | 'en'

const dictionary = en as Record<string, string>
const keys = Object.keys(dictionary).sort((a, b) => b.length - a.length)
const zhPattern = /[\u4e00-\u9fff]/

const queryLang = new URLSearchParams(window.location.search).get('lang')
if (queryLang === 'en' || queryLang === 'zh') {
  localStorage.setItem('nodehub_lang', queryLang)
}

export const locale = ref<Locale>(
  (localStorage.getItem('nodehub_lang') as Locale | null) === 'en' ? 'en' : 'zh',
)

const textOriginals = new WeakMap<Text, string>()
const attributeOriginals = new WeakMap<Element, Record<string, string>>()
let observer: MutationObserver | null = null
let applying = false

function translateValue(value: string): string {
  if (!zhPattern.test(value)) return value
  const leading = value.match(/^\s*/)?.[0] ?? ''
  const trailing = value.match(/\s*$/)?.[0] ?? ''
  const core = value.trim()
  if (!core) return value
  const exact = dictionary[core]
  if (exact) return leading + exact + trailing
  let result = core
  for (const key of keys) {
    if (key.length < 2 || !result.includes(key)) continue
    result = result.split(key).join(dictionary[key])
  }
  return leading + result + trailing
}

function translateTextNode(node: Text): void {
  const parent = node.parentElement
  if (parent?.closest('script,style,textarea,code,pre')) return
  const current = node.nodeValue
  if (!current || !zhPattern.test(current)) return
  if (!textOriginals.has(node)) textOriginals.set(node, current)
  node.nodeValue = translateValue(current)
}

function translateAttributes(element: Element): void {
  const attributes = ['placeholder', 'title', 'aria-label', 'alt']
  let originals = attributeOriginals.get(element)
  if (!originals) {
    originals = {}
    attributeOriginals.set(element, originals)
  }
  for (const name of attributes) {
    const value = element.getAttribute(name)
    if (!value || !zhPattern.test(value)) continue
    if (!(name in originals)) originals[name] = value
    element.setAttribute(name, translateValue(value))
  }
}

function walk(root: Node): void {
  if (root.nodeType === Node.TEXT_NODE) {
    translateTextNode(root as Text)
    return
  }
  if (!(root instanceof Element)) return
  const tag = root.tagName
  if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'TEXTAREA' || tag === 'CODE' || tag === 'PRE') return
  translateAttributes(root)
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  let node = walker.nextNode()
  while (node) {
    translateTextNode(node as Text)
    node = walker.nextNode()
  }
  root.querySelectorAll('*').forEach((element) => translateAttributes(element))
}

function restore(root: Node = document.body): void {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  let node = walker.nextNode()
  while (node) {
    const original = textOriginals.get(node as Text)
    if (original !== undefined) (node as Text).nodeValue = original
    node = walker.nextNode()
  }
  if (root instanceof Element) {
    root.querySelectorAll('*').forEach((element) => {
      const originals = attributeOriginals.get(element)
      if (!originals) return
      for (const [name, value] of Object.entries(originals)) element.setAttribute(name, value)
    })
  }
}

function startObserver(): void {
  if (observer) return
  observer = new MutationObserver((mutations) => {
    if (applying || locale.value !== 'en') return
    applying = true
    for (const mutation of mutations) {
      if (mutation.type === 'characterData') translateTextNode(mutation.target as Text)
      mutation.addedNodes.forEach((node) => walk(node))
    }
    applying = false
  })
  observer.observe(document.body, { childList: true, subtree: true, characterData: true })
}

export function setLocale(next: Locale): void {
  locale.value = next
  localStorage.setItem('nodehub_lang', next)
  document.documentElement.lang = next === 'en' ? 'en' : 'zh-Hant'
  if (next === 'en') {
    applying = true
    walk(document.body)
    applying = false
    startObserver()
  } else {
    restore(document.body)
  }
}

export function toggleLocale(): void {
  setLocale(locale.value === 'en' ? 'zh' : 'en')
}

export function installI18n(): void {
  document.documentElement.lang = locale.value === 'en' ? 'en' : 'zh-Hant'
  if (locale.value === 'en') {
    // Vue mounts asynchronously; defer one frame so the first screen is included.
    requestAnimationFrame(() => setLocale('en'))
  } else {
    startObserver()
  }
}
