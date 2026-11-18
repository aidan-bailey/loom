import "./styles/app.css";
import { createRoot } from "react-dom/client";

const mount = document.getElementById("app");
if (mount) createRoot(mount).render(<p>Loom</p>);
