// examples/generics_and_pool.ll
// Showcases monomorphized generics with interface bounds (`[T: Printable + Copyable]`),
// generic `Result[T, E]` from the core prelude, and the generational `Pool[T]` / `Handle[T]`
// from `std/pool.ll` (stale-handle safety + O(1) slot reuse).

import "std/pool.ll"
import "std/math.ll"

define Entity struct [
    name: string,
    hp: int,
    x: float,
    y: float,
]

bind Entity.to_string = (self) string: {
    "${self.name}(hp=${self.hp} @ ${self.x},${self.y})"
}

bind duplicate_and_format[T: Printable + Copyable] = (item: T) string: {
    let cloned = item.copy()
    "${item.to_string()} == ${cloned.to_string()}"
}

bind distance = (a: Entity, b: Entity) float: {
    hypot(b.x - a.x, b.y - a.y)
}

bind main = (): {
    // 1. Generic function with `Printable + Copyable` bounds
    let hero = Entity[name: "Ranger", hp: 100, x: 0.0, y: 0.0]
    let banner = duplicate_and_format(hero)
    assert(banner == "Ranger(hp=100 @ 0,0) == Ranger(hp=100 @ 0,0)")

    // 2. Generational Pool[Entity]
    let world = Pool[Entity].new()
    let h_player = world.alloc(own hero)
    let h_goblin = world.alloc(Entity[name: "Goblin", hp: 30, x: 3.0, y: 4.0])

    assert(world.len() == 2)
    assert(world.has(h_player))
    assert(world.has(h_goblin))

    let p1 = world.get(h_player) ?? panic("missing player")
    let g1 = world.get(h_goblin) ?? panic("missing goblin")
    assert(distance(p1, g1) == 5.0)

    // Free goblin -> `h_goblin` becomes stale!
    assert(world.free(h_goblin))
    assert(not world.has(h_goblin))
    assert(world.get(h_goblin) == none)
    assert(world.len() == 1)

    // Allocate dragon -> reuses goblin's physical slot (index 1) at generation 2!
    let h_dragon = world.alloc(Entity[name: "Dragon", hp: 500, x: 6.0, y: 8.0])
    assert(h_dragon.index == h_goblin.index)
    assert(h_dragon.generation == h_goblin.generation + 1)
    assert(world.capacity() == 2)

    // Stale `h_goblin` handle STILL safely returns `none`, never aliasing Dragon!
    assert(world.get(h_goblin) == none)
    let dragon_name = world.with[string](h_dragon, (e: Entity) string: { e.name.copy() }) ?? "none"
    assert(dragon_name == "Dragon")

    // 3. Generic Result[T] round-trip
    let ok_res = Result[int].ok(42)
    let err_res = Result[int].fail(Error.new("offline"))
    assert(ok_res.is_ok())
    assert(err_res.is_err())
    assert(ok_res.unwrap_or(0) == 42)
    assert(err_res.unwrap_or(-1) == -1)

    println("generics_and_pool: slot reused at gen=${h_dragon.generation}, occupant=$dragon_name")
}
