export default nextConfig;
const ArrowWithLocals = () => {
  const loc = 1;
  function nestedFn() {}
  const nestedArrow = () => { const deeper = 2; };
  return loc;
};
class Field {
  handler = () => { const inField = 1; function inFieldFn() {} };
  [Symbol.iterator]() {}
  "quoted-method"() {}
  [key: string]: any;
}
const keys = { "quoted-key": () => 1, 42: 1, [computed]: 2, short, ...spread };
namespace A.B.C { export const abc = 1; }
@Injectable()
export class Decorated {}
export const multi =
  () => 1;
xs.map(function namedCb() { const inCb = 1; });
items.forEach((x) => { function inAnon() {} const anonLocal = 1; });
export const veryLongCallbackHolder = someVeryLongFunctionNameThatIsReallyLongAndKeepsGoingForeverAndEverAndEverAndEverAndEverAndEverAndEverAndEverAndEver123456789(() => 1);
function outer() { return function returned() { const r = 1; }; }
let reassigned = 1;
reassigned = 2;
export async function* gen() {}
for (const loopVar of xs) {}
if (true) { const blockConst = 1; function blockFn() {} }
