import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Bundle is served by control-api at "/". Dev proxies /v1 to the API.
export default defineConfig({
  plugins: [react()],
  base: "/",
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    proxy: {
      "/v1": "http://127.0.0.1:9092",
    },
  },
});
