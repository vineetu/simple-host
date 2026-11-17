// Shared script for simple page behavior across the template site.
const navToggle = document.querySelector(".nav-toggle");
const siteNav = document.querySelector(".site-nav");
const themeToggle = document.querySelector(".theme-toggle");
const clock = document.querySelector("[data-clock]");
const storageKey = "template-theme";

if (navToggle && siteNav) {
  navToggle.addEventListener("click", () => {
    const isOpen = siteNav.classList.toggle("is-open");
    navToggle.setAttribute("aria-expanded", String(isOpen));
  });
}

// Persist theme preference so students can see local state in action.
const applyTheme = (theme) => {
  document.body.dataset.theme = theme;

  if (themeToggle) {
    themeToggle.textContent = theme === "dark" ? "Light mode" : "Dark mode";
  }
};

let savedTheme = null;

try {
  savedTheme = localStorage.getItem(storageKey);
} catch {
  savedTheme = null;
}

if (savedTheme) {
  applyTheme(savedTheme);
} else {
  applyTheme("light");
}

if (themeToggle) {
  themeToggle.addEventListener("click", () => {
    const nextTheme =
      document.body.dataset.theme === "dark" ? "light" : "dark";
    applyTheme(nextTheme);

    try {
      localStorage.setItem(storageKey, nextTheme);
    } catch {
      // Ignore storage errors so the example still works in restricted contexts.
    }
  });
}

// Update the clock once per second when the page includes a clock element.
if (clock) {
  const updateClock = () => {
    clock.textContent = new Date().toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  };

  updateClock();
  window.setInterval(updateClock, 1000);
}
