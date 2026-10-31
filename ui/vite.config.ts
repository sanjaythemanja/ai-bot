import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The build lands in ../web/dist, which the Go binary embeds.
// In dev (npm run dev), API and OAuth calls are proxied to the Go server.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: "../web/dist", emptyOutDir: true },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
      "/auth": "http://localhost:8080",
    },
  },
});
