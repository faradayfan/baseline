import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router-dom";
import "./index.css";
import { PrincipalProvider } from "./principal";
import AppShell from "./AppShell";
import FactsView from "./views/FactsView";
import FactDetailView from "./views/FactDetailView";
import InboxView from "./views/InboxView";
import PromotionDetailView from "./views/PromotionDetailView";
import NamespacesView from "./views/NamespacesView";
import ContextPreviewView from "./views/ContextPreviewView";

const router = createBrowserRouter([
  {
    path: "/",
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/facts" replace /> },
      { path: "facts", element: <FactsView /> },
      { path: "facts/:id", element: <FactDetailView /> },
      { path: "inbox", element: <InboxView /> },
      { path: "inbox/:id", element: <PromotionDetailView /> },
      { path: "namespaces", element: <NamespacesView /> },
      { path: "context", element: <ContextPreviewView /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PrincipalProvider>
      <RouterProvider router={router} />
    </PrincipalProvider>
  </StrictMode>,
);
