/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: { DEFAULT: "#111318", soft: "#171a21" },
        panel: { DEFAULT: "#1c2029", raised: "#222734" },
        border: { DEFAULT: "#2a3040" },
        accent: { DEFAULT: "#5b8cff", soft: "#7c5bff" },
        ok: "#3ecf8e",
        danger: "#ff5d6c",
        warn: "#ffb454",
        text: { DEFAULT: "#e6e9ef", dim: "#9aa3b2", faint: "#6b7280" },
      },
      borderRadius: { card: "12px" },
      boxShadow: { card: "0 6px 24px rgba(0,0,0,0.35)" },
    },
  },
  plugins: [],
};
