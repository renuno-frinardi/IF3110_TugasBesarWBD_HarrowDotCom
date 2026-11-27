import { useId, useRef } from "react";

export function ActionDropdown({ label, accessibleLabel, className, onClose, children }) {
  const menuId = useId();
  const triggerRef = useRef(null);

  function positionMenu(menu) {
    const trigger = triggerRef.current;
    const viewportHeight = document.documentElement.clientHeight;
    const gap = 8;
    if (viewportHeight - trigger.getBoundingClientRect().bottom < menu.offsetHeight + gap) {
      menu.dataset.needsRoom = "true";
      trigger.scrollIntoView({ block: "center", inline: "nearest" });
    }

    const { bottom, right } = trigger.getBoundingClientRect();
    menu.style.setProperty("--trigger-right", `${right}px`);
    menu.style.top = `${window.scrollY + bottom + gap}px`;
    menu.style.maxHeight = `${Math.max(44, viewportHeight - bottom - gap * 2)}px`;
    menu.dataset.positioned = "true";
  }

  return (
    <>
      <button ref={triggerRef} type="button" className="action-dropdown-toggle"
        popoverTarget={menuId} aria-label={accessibleLabel}>
        {label}
      </button>
      <div id={menuId} popover="auto" className={`action-dropdown ${className}`}
        onToggle={event => {
          const menu = event.currentTarget;
          if (event.nativeEvent.newState === "open") {
            positionMenu(menu);
          } else {
            delete menu.dataset.needsRoom;
            delete menu.dataset.positioned;
            menu.style.maxHeight = "";
            onClose?.();
          }
        }}>
        {children}
      </div>
    </>
  );
}
