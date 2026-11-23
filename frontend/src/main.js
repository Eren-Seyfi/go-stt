import { mount } from "svelte";
import "./main.css";
import { isSettingsWindow, loadSettings, loadTranscript } from "./lib/settings.svelte.js";
import App from "./App.svelte";
import SettingsApp from "./SettingsApp.svelte";

// The same bundle serves both windows: the main transcript window, and the
// settings window opened at /#/settings/<page>.
// Only the main window owns the transcript.
Promise.all([loadSettings(), isSettingsWindow ? null : loadTranscript()]).then(() =>
  mount(isSettingsWindow ? SettingsApp : App, { target: document.getElementById("app") }));
