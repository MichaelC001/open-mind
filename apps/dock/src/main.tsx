import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { PanelErrorBoundary } from "./panel/Panel";
import "./styles/global.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PanelErrorBoundary>
      <App />
    </PanelErrorBoundary>
  </StrictMode>,
);
