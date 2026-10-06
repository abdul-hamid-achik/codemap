import { describe, it, expect } from "vitest";

function buildFixture() {
  return { id: 1 };
}

describe("svc", () => {
  const helper = () => 1;
  it("works", () => {
    expect(helper()).toBe(1);
  });
  beforeEach(function setup() {});
});
