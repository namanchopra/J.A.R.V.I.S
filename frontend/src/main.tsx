import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import { initTheme } from './lib/theme'
import App from './App'

// Apply dark/light class before first paint to avoid flash
initTheme()

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <App/>
    </React.StrictMode>
)
