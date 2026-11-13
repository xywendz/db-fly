import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import { initSettings } from "./stores/settings";
import "./style.css";

initSettings();
createApp(App).use(createPinia()).mount("#app");
