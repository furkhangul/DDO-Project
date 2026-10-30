import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/source-serif-4/600.css";
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Layout } from "./components/Layout";
import { Loading, ToastHost } from "./components/ui";
import { ApiError, type Role } from "./lib/api";
import { homeFor, navItems, useUser } from "./lib/auth";
import { Login } from "./pages/Login";
import "./styles.css";

const Dashboard = lazy(() => import("./pages/Dashboard"));
const Tape = lazy(() => import("./pages/Tape"));
const Reservations = lazy(() => import("./pages/Reservations"));
const ReservationDetail = lazy(() => import("./pages/ReservationDetail"));
const NewReservation = lazy(() => import("./pages/NewReservation"));
const Allocation = lazy(() => import("./pages/Allocation"));
const Overbooking = lazy(() => import("./pages/Overbooking"));
const Housekeeping = lazy(() => import("./pages/Housekeeping"));
const Rooms = lazy(() => import("./pages/Rooms"));
const Pricing = lazy(() => import("./pages/Pricing"));
const NightAudit = lazy(() => import("./pages/NightAudit"));
const Reports = lazy(() => import("./pages/Reports"));
const Channels = lazy(() => import("./pages/Channels"));
const AuditLog = lazy(() => import("./pages/AuditLog"));
const GuestHome = lazy(() => import("./pages/GuestHome"));

const queryClient = new QueryClient({
  queryCache: new QueryCache(),
  mutationCache: new MutationCache(),
  defaultOptions: {
    queries: {
      staleTime: 5000,
      refetchOnWindowFocus: false,
      retry: (count, error) => !(error instanceof ApiError && error.status < 500) && count < 2,
    },
  },
});

const pages: Record<string, React.ReactNode> = {
  "/panel": <Dashboard />,
  "/cizelge": <Tape />,
  "/rezervasyonlar": <Reservations />,
  "/tahsis": <Allocation />,
  "/fazla-satis": <Overbooking />,
  "/kat-hizmetleri": <Housekeeping />,
  "/odalar": <Rooms />,
  "/fiyatlandirma": <Pricing />,
  "/gece-denetimi": <NightAudit />,
  "/raporlar": <Reports />,
  "/kanallar": <Channels />,
  "/denetim-kaydi": <AuditLog />,
  "/konaklamalarim": <GuestHome />,
};

function Guard({ roles, role, children }: { roles: Role[]; role: Role; children: React.ReactNode }) {
  return roles.includes(role) ? <>{children}</> : <Navigate to={homeFor(role)} replace />;
}

function App() {
  const user = useUser();
  if (!user) return <Login />;
  const bookers: Role[] = ["reception", "reservations", "manager"];
  const readers: Role[] = ["reception", "reservations", "revenue", "manager", "guest"];
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        <Route element={<Layout user={user} />}>
          {navItems.map((item) => (
            <Route
              key={item.to}
              path={item.to}
              element={
                <Guard roles={item.roles} role={user.role}>
                  {pages[item.to]}
                </Guard>
              }
            />
          ))}
          <Route path="/rezervasyonlar/yeni" element={<Guard roles={bookers} role={user.role}><NewReservation /></Guard>} />
          <Route path="/rezervasyonlar/:id" element={<Guard roles={readers} role={user.role}><ReservationDetail /></Guard>} />
          <Route path="*" element={<Navigate to={homeFor(user.role)} replace />} />
        </Route>
      </Routes>
    </Suspense>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ToastHost>
          <App />
        </ToastHost>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
