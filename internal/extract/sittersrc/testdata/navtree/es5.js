const x = 1;
module.exports = { run() {}, val: 1 };
exports.helper = function () { return 1; };
exports.arrowHelper = () => 2;
function foo() {}
exports.foo = foo;
function Ctor() {}
Ctor.prototype.method = function () {};
Ctor.prototype = { a() {}, b: 1 };
Ctor.make = function () {};
function el(tag) {
  const node = {
    tag,
    getAttribute(name) { return name; },
  };
  Object.defineProperty(node, "textContent", {
    get() { return ""; },
  });
  return node;
}
/**
 * @typedef {Object} Migration
 * @property {Object} filter - the filter
 */

/** Runs a migration. */
const migrate = (name, migration = () => {}) => async (db) => {
  const { a, b } = db;
};
