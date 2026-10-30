import { useSyncExternalStore } from "react";
import { currentUser, onSessionChange, type Role, type User } from "./api";

export function useUser(): User | null {
  return useSyncExternalStore(onSessionChange, currentUser);
}

export interface NavItem {
  to: string;
  label: string;
  roles: Role[];
}

const everyStaff: Role[] = ["reception", "reservations", "housekeeping", "revenue", "manager"];

export const navItems: NavItem[] = [
  { to: "/panel", label: "Panel", roles: ["reception", "reservations", "revenue", "manager"] },
  { to: "/cizelge", label: "Oda Çizelgesi", roles: ["reception", "reservations", "manager"] },
  { to: "/rezervasyonlar", label: "Rezervasyonlar", roles: ["reception", "reservations", "revenue", "manager"] },
  { to: "/tahsis", label: "Oda Tahsisi", roles: ["reception", "reservations", "manager"] },
  { to: "/fazla-satis", label: "Fazla Satış", roles: ["reception", "reservations", "manager"] },
  { to: "/kat-hizmetleri", label: "Kat Hizmetleri", roles: ["housekeeping", "reception", "manager"] },
  { to: "/odalar", label: "Odalar", roles: everyStaff },
  { to: "/fiyatlandirma", label: "Fiyatlandırma", roles: ["reception", "reservations", "revenue", "manager"] },
  { to: "/gece-denetimi", label: "Gece Denetimi", roles: ["revenue", "manager"] },
  { to: "/raporlar", label: "Raporlar", roles: ["revenue", "manager"] },
  { to: "/kanallar", label: "Kanallar", roles: ["reception", "reservations", "revenue", "manager"] },
  { to: "/denetim-kaydi", label: "Denetim Kaydı", roles: ["manager"] },
  { to: "/konaklamalarim", label: "Konaklamalarım", roles: ["guest"] },
];

export function homeFor(role: Role) {
  if (role === "guest") return "/konaklamalarim";
  if (role === "housekeeping") return "/kat-hizmetleri";
  return "/panel";
}

export const can = {
  folio: (role?: Role) => role === "reception" || role === "revenue" || role === "manager",
  desk: (role?: Role) => role === "reception" || role === "manager",
  book: (role?: Role) => role === "reception" || role === "reservations" || role === "manager",
  price: (role?: Role) => role === "revenue" || role === "manager",
};
