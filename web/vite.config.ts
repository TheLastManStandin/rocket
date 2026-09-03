import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development Vite serves the app and forwards the API and the socket to the
// Go server. In production Go serves the built bundle itself, so there is only
// ever one origin -- which is all a Telegram Mini App can be pointed at.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8090", changeOrigin: true },
      "/ws": { target: "ws://localhost:8090", ws: true },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
