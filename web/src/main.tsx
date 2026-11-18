import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { App } from "./App";
import { Starfield } from "./components/Starfield";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Starfield />
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
