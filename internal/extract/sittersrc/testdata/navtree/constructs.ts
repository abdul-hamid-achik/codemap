import { x } from "./x";
// Doc for top.
export function top(a: number): number {
  const local = 1;
  function inner() { return local; }
  const innerArrow = () => inner();
  return a + innerArrow();
}
export const arrow = (b: string) => b.length;
export const fnExpr = function named() { return 1; };
const obj = {
  m() { return 1; },
  p: () => 2,
  q: function () { return 3; },
  v: 4,
  nested: { deep() {} },
};
let a1 = 1, b1 = "x";
const { d1, e1 } = obj as any;
export default function () { return 0; }
export class Svc extends Base {
  field = 1;
  arrowField = () => this.field;
  static s = 2;
  private readonly r: number;
  constructor(r: number) { super(); this.r = r; }
  get g() { return 1; }
  set g(v) {}
  method(): void { const z = () => 1; }
  static stat() {}
  #priv() {}
}
abstract class Abs { abstract am(): void; }
export interface I { prop: string; meth(): void; (call: number): void; }
export type T = { a: number; f(): void };
export type U = string | number;
export enum E { A, B = 2 }
declare module "mod" { export function mf(): void; }
namespace NS { export const nv = 1; export function nf() {} }
describe("suite", () => {
  const helper = () => 1;
  it("works", () => { helper(); });
  beforeEach(function setup() {});
});
app.get("/x", (req, res) => { const q = 1; });
export const Comp = React.memo(function Comp() { return null; });
export const wrapped = wrap(() => 1);
const cls = class Named { m() {} };
function overload(a: string): void;
function overload(a: number): void;
function overload(a: any) {}
export * from "./y";
export { x as xx };
