import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LanguageProvider } from "./i18n";
import { RootBoundary } from "./RootBoundary";

function Broken(): never {
  throw new Error("networks is undefined");
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("RootBoundary", () => {
  it("shows an error screen with a reload button instead of a white page", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const reload = vi.fn();
    render(
      <RootBoundary reload={reload}>
        <Broken />
      </RootBoundary>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Something went wrong");
    expect(screen.getByText("networks is undefined")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reload the page" }));
    expect(reload).toHaveBeenCalled();
  });

  it("speaks Italian", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    window.localStorage.setItem("regulus.language", "it");
    render(
      <LanguageProvider>
        <RootBoundary>
          <Broken />
        </RootBoundary>
      </LanguageProvider>,
    );
    expect(screen.getByRole("button", { name: "Ricarica la pagina" })).toBeInTheDocument();
  });

  it("renders its children when nothing fails", () => {
    render(
      <RootBoundary>
        <p>fine</p>
      </RootBoundary>,
    );
    expect(screen.getByText("fine")).toBeInTheDocument();
  });
});
