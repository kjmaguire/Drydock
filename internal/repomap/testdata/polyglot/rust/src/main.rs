mod engine;

use engine::Runner;

struct Car;

impl Runner for Car {
    fn run(&self) {
        let e = engine::build_engine();
        e.start_engine();
    }
}

fn main() {
    Car.run();
}
