import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "@/app";

import "@/app/styles/index.css";

const container = document.getElementById("root");
if (!container) {
  throw new Error("root container is missing");
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>
);
