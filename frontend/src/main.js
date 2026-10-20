import { mount } from 'svelte'
import '@fontsource-variable/inter/wght.css'
import '@fontsource-variable/jetbrains-mono/wght.css'
import App from './App.svelte'
import './app.css'

const app = mount(App, {
  target: document.getElementById('app'),
})

export default app
