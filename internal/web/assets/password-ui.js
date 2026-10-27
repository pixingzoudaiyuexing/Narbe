// Cloud login passwords remain in their form only. Never persist them or put
// their value into an attribute, summary, task result, or visibility control.
export function cloudPasswordInput(attributes = "") {
  return `<span class="password-entry"><input name="password" type="text" autocomplete="new-password" spellcheck="false" autocapitalize="off" ${attributes}><button type="button" class="quiet password-toggle" data-password-toggle aria-label="隐藏密码" aria-pressed="true"><svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></svg><span data-password-label>隐藏</span></button></span>`;
}

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-password-toggle]");
  if (!button) return;
  const input = button.closest(".password-entry")?.querySelector("input");
  if (!input) return;
  const visible = input.type !== "text";
  input.type = visible ? "text" : "password";
  button.setAttribute("aria-pressed", String(visible));
  button.setAttribute("aria-label", visible ? "隐藏密码" : "显示密码");
  button.querySelector("[data-password-label]").textContent = visible
    ? "隐藏"
    : "显示";
});
