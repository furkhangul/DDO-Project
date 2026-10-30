import { useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { logout, type User } from "../lib/api";
import { navItems } from "../lib/auth";
import { dayLong, roleLabels } from "../lib/format";

export function Layout({ user }: { user: User }) {
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const [seen, setSeen] = useState(location.pathname);
  if (seen !== location.pathname) {
    setSeen(location.pathname);
    setOpen(false);
  }
  const items = navItems.filter((item) => item.roles.includes(user.role));
  return (
    <div className="shell">
      <aside className={open ? "side open" : "side"}>
        <div className="brand">
          <img src="/konak.svg" alt="" width={30} height={30} />
          <div>
            <strong>KonakPro</strong>
            <span>Konak Palas Otel</span>
          </div>
          <button className="menu-toggle" onClick={() => setOpen(!open)} aria-expanded={open} aria-label="Menü">
            ☰
          </button>
        </div>
        <nav>
          {items.map((item) => (
            <NavLink key={item.to} to={item.to}>
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="side-user">
          <strong>{user.full_name}</strong>
          <span>{roleLabels[user.role]}</span>
          <button className="link" onClick={() => void logout()}>
            Çıkış yap
          </button>
        </div>
      </aside>
      <main>
        <Outlet />
      </main>
    </div>
  );
}

export const BusinessDate = ({ value }: { value: string }) => <span className="business-date">Otel günü: {dayLong(value)}</span>;
