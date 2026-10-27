import { createApp } from "vue";
import App from "./App.vue";
import "./style.css";
import { applyThemeCache } from "./store";

applyThemeCache();
createApp(App).mount("#app");
